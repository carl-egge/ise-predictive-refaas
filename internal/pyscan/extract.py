"""Deterministic AST analysis of a single Python serverless function.

Reads Python source on stdin, writes one JSON object to stdout. Emits *raw
facts only* - import names, construct counts, complexity metrics. Every
policy decision (which third-party libraries map to which Go packages, which
are infeasible, the one-hot vocabulary, the feature vector's key order) lives
on the Go side in internal/pyscan, so the mapping table can be edited without
touching the parser and both consumers - [C8]'s prompt hints and [I3]'s model
features - are computed from one parse of one source of truth.

Contract: exit 0 with a single JSON object on stdout, or exit non-zero with a
diagnostic on stderr and nothing on stdout. Never writes anything but that
object to stdout; the Go side parses it whole.

Complexity definitions are calibrated against the evaluation dataset, whose
meta.json was produced with radon (its cc_rank/h_* fields are radon's). Over
evaluation_set's 95 functions this file's `cc` reproduces meta.json exactly
for 92 and within 2 for 94 (Pearson r = 0.9998); `lloc` correlates at
r = 0.9936 with a systematic offset of about -4 lines, so it measures the
same quantity on a slightly different basis rather than matching byte for
byte. See internal/pyscan/calibration_test.go, which pins both.
"""

import ast
import io
import json
import sys
import tokenize

SCHEMA_VERSION = 1

# Modules that ship with CPython. sys.stdlib_module_names exists on 3.10+;
# the fallback covers older interpreters. Anything not in here and not
# relative is treated as third-party.
try:
    STDLIB = set(sys.stdlib_module_names)
except AttributeError:  # pragma: no cover - only on <3.10
    STDLIB = {
        "abc", "argparse", "ast", "asyncio", "base64", "binascii", "bisect",
        "builtins", "bz2", "calendar", "cgi", "cmath", "collections",
        "concurrent", "configparser", "contextlib", "copy", "csv", "ctypes",
        "dataclasses", "datetime", "decimal", "difflib", "dis", "email",
        "enum", "errno", "functools", "gc", "getpass", "gzip", "hashlib",
        "heapq", "hmac", "html", "http", "imp", "importlib", "inspect", "io",
        "ipaddress", "itertools", "json", "logging", "math", "mimetypes",
        "operator", "os", "pathlib", "pickle", "platform", "pprint", "queue",
        "random", "re", "secrets", "shutil", "signal", "smtplib", "socket",
        "sqlite3", "ssl", "stat", "string", "struct", "subprocess", "sys",
        "tempfile", "textwrap", "threading", "time", "timeit", "traceback",
        "types", "typing", "unicodedata", "unittest", "urllib", "uuid",
        "warnings", "weakref", "xml", "zipfile", "zlib",
    }

# boto3 factory calls whose first positional argument names an AWS service.
BOTO3_FACTORIES = {"client", "resource"}

# Builtins whose use signals reflection or dynamic dispatch - the constructs
# with no direct Go equivalent, which a translation has to work around.
DYNAMIC_BUILTINS = frozenset({
    "eval", "exec", "compile", "getattr", "setattr", "delattr", "hasattr",
    "globals", "locals", "vars", "__import__",
})


def logical_lines(source):
    """Count logical lines: one per NEWLINE token, which ends a logical (not
    physical) line, so a statement wrapped across brackets counts once and
    blank/comment lines count not at all."""
    count = 0
    try:
        for tok in tokenize.generate_tokens(io.StringIO(source).readline):
            if tok.type == tokenize.NEWLINE:
                count += 1
    except (tokenize.TokenError, IndentationError):
        # The AST parse is the authority on validity; a tokenizer hiccup on
        # otherwise-parseable source should not fail the whole scan.
        pass
    return count


class Visitor(ast.NodeVisitor):
    """One pass collecting every fact the feature vector and the prompt hints
    need, so the source is parsed once regardless of consumer count.

    Cyclomatic complexity is accumulated per *block* (function/lambda), the
    way radon reports it, because the dataset's `cc` is the maximum over
    blocks rather than a whole-file sum. Both aggregates are emitted: `cc`
    (max) is the radon-comparable one, `cc_total` describes the whole file
    and carries genuinely different information for the model.
    """

    def __init__(self):
        # complexity: module-level accumulator plus one entry per block
        self._module_cc = 0
        self._blocks = []
        self._cur = None
        self.max_depth = 0
        self._depth = 0
        # structure
        self.n_defs = 0
        self.n_async_defs = 0
        self.n_classes = 0
        self.n_lambdas = 0
        self.n_branches = 0
        self.n_loops = 0
        self.n_try = 0
        self.n_except = 0
        self.n_raise = 0
        self.n_with = 0
        self.n_global = 0
        self.n_comprehensions = 0
        self.n_yield = 0
        self.n_await = 0
        self.n_decorators = 0
        self.n_star_args = 0
        self.n_kwargs = 0
        self.n_returns = 0
        self.n_fstrings = 0
        self.dynamic_calls = {}
        # imports
        self.imports = set()
        self.relative_imports = 0
        self.boto3_services = set()
        # halstead basis
        self.operators = []
        self.operands = []
        # statements outside any def/class run at import time in Lambda,
        # which is a known translation hazard worth a feature of its own
        self.module_level_stmts = 0
        self._in_def = 0
        self.top_level_functions = []

    # -- complexity plumbing ---------------------------------------------

    def _cc(self, k=1):
        if self._cur is None:
            self._module_cc += k
        else:
            self._cur[1] += k

    def _open_block(self, name):
        block = [name, 1]
        self._blocks.append(block)
        previous = self._cur
        self._cur = block
        return previous

    def _descend(self, node):
        self._depth += 1
        self.max_depth = max(self.max_depth, self._depth)
        self.generic_visit(node)
        self._depth -= 1

    def _stmt(self, node):
        if self._in_def == 0 and not isinstance(
            node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)
        ):
            self.module_level_stmts += 1

    # -- definitions ------------------------------------------------------

    def visit_FunctionDef(self, node):
        self._function(node)

    def visit_AsyncFunctionDef(self, node):
        self.n_async_defs += 1
        self._function(node)

    def _function(self, node):
        self._stmt(node)
        self.n_defs += 1
        if self._in_def == 0:
            self.top_level_functions.append(node.name)
        self.n_decorators += len(node.decorator_list)
        args = node.args
        if args.vararg is not None:
            self.n_star_args += 1
        if args.kwarg is not None:
            self.n_kwargs += 1
        self.operands.append(node.name)
        for arg in args.posonlyargs + args.args + args.kwonlyargs:
            self.operands.append(arg.arg)

        previous = self._open_block(node.name)
        self._in_def += 1
        self._descend(node)
        self._in_def -= 1
        self._cur = previous

    def visit_Lambda(self, node):
        self.n_lambdas += 1
        previous = self._open_block("<lambda>")
        self.generic_visit(node)
        self._cur = previous

    def visit_ClassDef(self, node):
        self._stmt(node)
        self.n_classes += 1
        self.n_decorators += len(node.decorator_list)
        self.operands.append(node.name)
        self._in_def += 1
        self._descend(node)
        self._in_def -= 1

    # -- decision points ---------------------------------------------------

    def visit_If(self, node):
        self._stmt(node)
        self.n_branches += 1
        self._cc()
        self.operators.append("if")
        self._descend(node)

    def visit_IfExp(self, node):
        self._cc()
        self.operators.append("ifexp")
        self.generic_visit(node)

    def visit_For(self, node):
        self._loop(node, "for")

    def visit_AsyncFor(self, node):
        self._loop(node, "async for")

    def visit_While(self, node):
        self._loop(node, "while")

    def _loop(self, node, name):
        self._stmt(node)
        self.n_loops += 1
        # radon charges an extra decision for a loop's else clause
        self._cc(2 if node.orelse else 1)
        self.operators.append(name)
        self._descend(node)

    def visit_Try(self, node):
        self._stmt(node)
        self.n_try += 1
        self.operators.append("try")
        self._descend(node)

    def visit_ExceptHandler(self, node):
        self.n_except += 1
        self._cc()
        self.operators.append("except")
        self._descend(node)

    def visit_With(self, node):
        self._stmt(node)
        self.n_with += 1
        self.operators.append("with")
        self._descend(node)

    visit_AsyncWith = visit_With

    def visit_Assert(self, node):
        self._stmt(node)
        self._cc()
        self.operators.append("assert")
        self.generic_visit(node)

    def visit_BoolOp(self, node):
        # each additional operand in an and/or chain is one more decision
        self._cc(len(node.values) - 1)
        self.operators.append(type(node.op).__name__)
        self.generic_visit(node)

    def _comprehension(self, node):
        self.n_comprehensions += 1
        for gen in node.generators:
            self._cc(1 + len(gen.ifs))
        self.generic_visit(node)

    visit_ListComp = _comprehension
    visit_SetComp = _comprehension
    visit_DictComp = _comprehension
    visit_GeneratorExp = _comprehension

    # -- plain statements ---------------------------------------------------

    def visit_Raise(self, node):
        self._stmt(node)
        self.n_raise += 1
        self.operators.append("raise")
        self.generic_visit(node)

    def visit_Return(self, node):
        self._stmt(node)
        self.n_returns += 1
        self.operators.append("return")
        self.generic_visit(node)

    def visit_Global(self, node):
        self._stmt(node)
        self.n_global += 1
        self.generic_visit(node)

    visit_Nonlocal = visit_Global

    def visit_Assign(self, node):
        self._stmt(node)
        self.operators.append("=")
        self.generic_visit(node)

    def visit_AugAssign(self, node):
        self._stmt(node)
        self.operators.append(type(node.op).__name__ + "=")
        self.generic_visit(node)

    def visit_AnnAssign(self, node):
        self._stmt(node)
        self.operators.append("=")
        self.generic_visit(node)

    def visit_Expr(self, node):
        self._stmt(node)
        self.generic_visit(node)

    def visit_Delete(self, node):
        self._stmt(node)
        self.generic_visit(node)

    def visit_Pass(self, node):
        self._stmt(node)

    def visit_Break(self, node):
        self._stmt(node)

    def visit_Continue(self, node):
        self._stmt(node)

    # -- generators / async --------------------------------------------------

    def visit_Yield(self, node):
        self.n_yield += 1
        self.operators.append("yield")
        self.generic_visit(node)

    visit_YieldFrom = visit_Yield

    def visit_Await(self, node):
        self.n_await += 1
        self.operators.append("await")
        self.generic_visit(node)

    # -- imports ---------------------------------------------------------------

    def visit_Import(self, node):
        self._stmt(node)
        for alias in node.names:
            self.imports.add(alias.name.split(".")[0])
        self.generic_visit(node)

    def visit_ImportFrom(self, node):
        self._stmt(node)
        if node.level:
            # relative import; the dataset inlines repo-local modules, so
            # these are rare and are certainly not third-party
            self.relative_imports += 1
        elif node.module:
            self.imports.add(node.module.split(".")[0])
        self.generic_visit(node)

    # -- calls -------------------------------------------------------------------

    def visit_Call(self, node):
        func = node.func
        name = None
        if isinstance(func, ast.Name):
            name = func.id
        elif isinstance(func, ast.Attribute):
            name = func.attr
            self._boto3_service(func, node)
        if name:
            self.operators.append(name)
            if name in DYNAMIC_BUILTINS:
                self.dynamic_calls[name] = self.dynamic_calls.get(name, 0) + 1
        for kw in node.keywords:
            if kw.arg is None:
                self.n_kwargs += 1
        for arg in node.args:
            if isinstance(arg, ast.Starred):
                self.n_star_args += 1
        self.generic_visit(node)

    def _boto3_service(self, func, call):
        """Record boto3.client("s3") / session.resource("dynamodb")."""
        if func.attr not in BOTO3_FACTORIES or not call.args:
            return
        first = call.args[0]
        if isinstance(first, ast.Constant) and isinstance(first.value, str):
            self.boto3_services.add(first.value.lower())

    # -- leaves for halstead --------------------------------------------------------

    def visit_Name(self, node):
        self.operands.append(node.id)
        self.generic_visit(node)

    def visit_Constant(self, node):
        self.operands.append(repr(node.value))
        self.generic_visit(node)

    def visit_JoinedStr(self, node):
        self.n_fstrings += 1
        self.generic_visit(node)

    def visit_BinOp(self, node):
        self.operators.append(type(node.op).__name__)
        self.generic_visit(node)

    def visit_UnaryOp(self, node):
        self.operators.append(type(node.op).__name__)
        self.generic_visit(node)

    def visit_Compare(self, node):
        for op in node.ops:
            self.operators.append(type(op).__name__)
        self.generic_visit(node)

    # -- aggregates -------------------------------------------------------------------

    @property
    def cc_max(self):
        """Highest complexity of any single block, or the module's own when
        the file defines no functions. This is the dataset's `cc`."""
        blocks = [c for _, c in self._blocks]
        return max(blocks + [self._module_cc + 1])

    @property
    def cc_total(self):
        return sum(c for _, c in self._blocks) + self._module_cc


def halstead(operators, operands):
    """Halstead metrics on a broader basis than radon's: every call target and
    identifier counts, not only arithmetic/comparison operators. They are
    therefore NOT comparable to meta.json's h_* fields (which are ~20x
    smaller) and are deliberately named differently to keep the two apart -
    these are model features, not a reproduction.
    """
    n1, n2 = len(set(operators)), len(set(operands))
    total1, total2 = len(operators), len(operands)
    difficulty = (n1 / 2) * (total2 / n2) if n2 else 0.0
    return {
        "halstead_vocabulary": n1 + n2,
        "halstead_length": total1 + total2,
        "halstead_difficulty": round(difficulty, 6),
    }


def analyze(source):
    tree = ast.parse(source)
    v = Visitor()
    v.visit(tree)

    third_party = sorted(m for m in v.imports if m not in STDLIB)
    stdlib_used = sorted(m for m in v.imports if m in STDLIB)
    lloc = logical_lines(source)

    metrics = {
        "lloc": lloc,
        "cc": v.cc_max,
        "cc_total": v.cc_total,
        "max_nesting_depth": v.max_depth,
        "n_defs": v.n_defs,
        "n_async_defs": v.n_async_defs,
        "n_classes": v.n_classes,
        "n_lambdas": v.n_lambdas,
        "n_branches": v.n_branches,
        "n_loops": v.n_loops,
        "n_try": v.n_try,
        "n_except": v.n_except,
        "n_raise": v.n_raise,
        "n_with": v.n_with,
        "n_global": v.n_global,
        "n_comprehensions": v.n_comprehensions,
        "n_yield": v.n_yield,
        "n_await": v.n_await,
        "n_decorators": v.n_decorators,
        "n_star_args": v.n_star_args,
        "n_kwargs": v.n_kwargs,
        "n_returns": v.n_returns,
        "n_fstrings": v.n_fstrings,
        "n_imports": len(v.imports),
        "n_third_party": len(third_party),
        "n_relative_imports": v.relative_imports,
        "module_level_stmts": v.module_level_stmts,
        "source_lines": len(source.splitlines()),
    }
    metrics.update(halstead(v.operators, v.operands))

    return {
        "schema_version": SCHEMA_VERSION,
        "metrics": metrics,
        "imports": sorted(v.imports),
        "third_party_imports": third_party,
        "stdlib_imports": stdlib_used,
        "boto3_services": sorted(v.boto3_services),
        "dynamic_calls": v.dynamic_calls,
        "top_level_functions": v.top_level_functions,
    }


def main():
    source = sys.stdin.read()
    try:
        result = analyze(source)
    except SyntaxError as exc:
        print("pyscan: cannot parse source: %s" % exc, file=sys.stderr)
        return 2
    except RecursionError:
        print("pyscan: nesting exceeds the parser's recursion limit", file=sys.stderr)
        return 3
    json.dump(result, sys.stdout, sort_keys=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
