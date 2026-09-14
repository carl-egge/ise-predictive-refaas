# Threats to validity and discussion points

Grounded in this project specifically, not generic. Every item names the mechanism or the
number it rests on. Marked (§3.3) where the existing text already acknowledges it.

## Construct validity: does the measurement measure the claim?

**C1. Inference energy is modeled, not measured.** Every headline energy figure is token
counts multiplied by coefficients derived from a hardware and serving model (2xH200, FP8,
batch 32, 1700 W node, PUE 1.05). Only the pipeline host's own draw is RAPL-measured, and
it is about 2% of the total (1.4% to 2.4% of run spend across the three replicate runs). GWDG confirmed the hardware and carbon-neutral operation but
permanently declined to disclose concurrency. *Mitigation:* the sensitivity sweep varies
concurrency (8 to 128), node power (1400 to 2550 W), peak FLOPs and bytes per parameter;
report the resulting range, not a point estimate. This is the single largest threat in the
thesis and should be named first.

**C2. "Validated" means "passes its fixtures", and the fixtures assert observed
behavior.** Expected outputs were recorded from the original function, so a buggy original
produces a test that asserts the bug. Correct for measuring equivalence, wrong for reading
the corpus as a set of correct functions. (§3.3)

**C3. The default comparison mode is lenient.** `tolerant` performs structural subset
matching with lenient scalars, so extra response fields are ignored and `"3"` matches `3`.
A stricter equivalence criterion would lower every reported success rate. State the
per-mode case counts.

**C4. Test coverage per function is thin.** 392 fixtures over 95 functions, about four
cases each. Passing four black-box cases is weak evidence of behavioral equivalence, and
side-effect assertions exist only for services the emulator can be queried for.

**C5. Six functions have external API call sites that no test exercises**, because the
harness blocks outbound network. Translation defects inside those call sites are invisible
to the benchmark. (§3.3)

**C6. The emulator is not AWS.** Validation establishes equivalence against an emulator's
implementation of the AWS API. Divergence between emulator and provider is unmeasured.

**C7. The runtime comparison measures whichever branch the fixtures reach.** No fixture
sets an environment variable (0 of 392), although 15 of 95 functions read one, so some
suites exercise only an error branch. All three fixtures of f30, including the one named
happy-path, expect the error response the original returns when `DESTINATION_BUCKET` is
unset. A translation that checks the variable before building its S3 client then measures
0.19 mJ per invocation where one that attempts the call measures 18 mJ, for identical
observable behaviour (runs 20260912-172904 and 20260904-190539). Four such early-exit
translations (f20, f30, f56, f72: Go below 1 mJ while Python is above 10 mJ, with none in the
other two runs) are why run 20260912-172904 shows a median steady-state energy ratio of 1.8x
against 1.3x on the other two runs; without them it is 1.25x. Per-function speedups therefore partly
measure how a translation handles missing configuration; savings, and N*, less so, because
the Python side's cost dominates the difference.

## Internal validity: could something else explain the result?

**I1. Outcomes are stochastic, and by how much is now measured.** Three full runs of the
frozen configuration (20260904-190539, 20260911-165103, 20260912-172904: one config sha,
code differing only by a comment) agree pairwise on 82% to 85% of labels (Cohen kappa 0.64
to 0.71, Fleiss kappa 0.68). 23 of 95 functions (24%) change outcome at least once, 34
always pass and 38 never do, and the success count moves 47 / 43 / 46. Across the
configuration change (20260831-190900 to 20260904-190539) agreement was 76% (kappa 0.515,
23 flipped), so most of that churn is sampling rather than configuration. Translation
samples at temperature 0.1, the two repair stages at 0.5 (top-p 0.95).

**I2. Configuration changes were bundled.** Between 20260831-190900 and 20260904-190539 the
cleaner stage was replaced by a summary stage, exact-module AWS hints were added, the
stagnation comparison was normalized, `go mod tidy` diagnostics were made causal, and the
Floci-deployed Lambda was given a reachable `AWS_ENDPOINT_URL` ([C11a]). The Floci route's
pass rate among buildable functions rose from 18% to 44% (45% to 52% on the replicates),
consistent with that last fix. The net gain of five functions is a bundle effect, and
against a within-configuration spread of 43 to 47 successes it is not distinguishable from
sampling; no per-change ablation exists.

**I3. The D+ regression is a plausible mechanism, not an isolated cause.** D+ fell from
7/20 to 3/20 while the cleaner stage was removed, and stayed low on the replicates (3, 0
and 5; mean 2.7). Of the four D+ functions lost, two went from compiling (f9 on the first
attempt, f93 after one repair) to never reaching the test stage. Suggestive; not an
ablation, and the earlier 7 is itself a single run.

**I4. Thresholds were tuned on a run that is also reported.** The retry budgets and the
2/3 stagnation thresholds were selected by replaying run 20260831-190900.

**I5. The predictor is evaluated in-corpus, and its only out-of-corpus test is tiny.**
The reported models are fitted and cross-validated on the three replicate runs, with folds
drawn over functions and grouped on near-duplicates, so no function is scored by a model
that saw it. Out of corpus there is only function_set: 14 functions, none using AWS, where
the logistic regression reaches AUC 0.70 while translating all 14 and the random forest
0.39. The earlier transfer test (the model fitted on 20260831-190900, scored on the
replicates) is label-held-out but not feature-held-out: it reuses the same functions with
new labels, and 76% of those labels are unchanged.

## External validity: does it generalize?

**E1. N = 95, effective N = 86** after near-duplicate grouping (16 functions in 7 groups,
4 of them crossing repository boundaries).

**E2. One language pair, one model, one provider, one emulator.**

**E3. Training-data contamination is plausible and unmeasured.** The corpus is scraped
from The Stack (cutoff March 2022), a standard pretraining corpus.

**E4. The energy win does not generalize across the corpus.** It is confined to the AWS
subset: median 21.7 J versus -0.001 J saved per 1000 invocations, Mann-Whitney
p = 1.2e-5, with 13 of 23 non-AWS functions slightly worse in Go. It replicates: on the
two further runs the AWS medians are 28.0 J and 57.6 J against -0.035 J and -0.018 J
(Mann-Whitney p = 6.8e-6 and 1.2e-5). "Python to Go saves energy" holds for SDK- and
I/O-heavy functions, not for this corpus as a whole.

**E5. D+ is both the hardest bucket and the thinnest**, at 20 functions rather than 25,
because the corpus contained only 437 candidates above complexity 20 in total.

**E6. Single-file functions only.** Repository-level translation, where the practical
demand is, is out of scope. (§3.2)

## Conclusion validity: are the numbers doing the work claimed?

**N1. The break-even distribution is too wide for its median.** N* spans 14,420 to
6.7e8 over 31 functions; a median near 1.0e6 summarizes almost nothing. The replicates
sharpen the point: the same configuration gives medians of 1.0e6, 4.5e5 and 1.3e5 (4.0e5
without the four early-exit translations of C7), while the
cheapest functions (f76, f77, f45, f30) repay between 1.4e4 and 3.9e4 invocations every time.
The instability is in which functions repay, not in any one function's break-even: the 27
functions that repay in at least two runs differ between their highest and lowest run by a
median factor of 1.6, all within a factor of ten, while only 55 functions are validated in
any run and 10 repay in one run and never in another. Report the distribution, not the
median.

**N2. N* is computed only where Go is faster.** 15 of 46 validated translations are not
faster in steady state and never repay (11 of 40 and 16 of 45 on the replicates); excluding them biases the median optimistic. State
the count alongside the median.

**N3. Cold start and steady state disagree by roughly a factor of 35.** Go is about 46x
cheaper on cold start and about 1.3x on steady-state energy (45x and 1.3x, 47x and 1.8x on
the replicates; C7 explains the last steady-state figure). N* deliberately uses steady
state, since a function invoked N times is mostly warm, but the choice decides whether
translation reads as transformative or marginal.

**N4. The permutation test is only marginally significant.** On the replicate series the
logistic regression's AUC of 0.642 against a group-permutation null of 0.507 +- 0.076
gives p = 0.040 over 200 permutations. The single-run model's p = 0.010 sat near the 1/201
resolution floor; this one is clear of the floor but only just below 5%. The signal is
real at the conventional level, not comfortably.

**N5. The shipped gate has no robust useful range; the better model does.** A gate is
useful where it beats both baselines: net-positive, and better than translating everything.
The shipped logistic regression, retrained on the replicates and applied through its
out-of-fold decisions, has that window only in two runs (8.4e5 to 9.9e5 and 6.8e5 to 9.9e5)
and none in the third. The random forest has it in all three, from 2.2e5 to 2.7e5 up to
1.7e6 to 4.6e6 invocations. Both ends scale with the same energy constants, so whether the
window exists does not depend on C1; it depends on which functions the gate declines and
which of them happen to succeed. The deployed gate is the random forest; the Go reader
reproduces its probabilities to 1e-16, so the window describes the model that ships.

**N6. A measurement defect was found and corrected, and disclosing it is a strength.** An
earlier runtime measurement timed 13 translations that had never passed their tests,
because the benchmark script archives the package of a failed job as evidence. Filtering
against the run log corrected it. N* was unaffected, since it joins on completed
translations, but the descriptive Go-versus-Python steady-state ratio moved from 1.66x to
1.28x.

## Discussion points

**D1. A gate learns a pipeline version, not translatability.** Scored against a later run
(20260904-190539), 19 of the shipped model's 24 errors were exactly the functions whose
label had flipped, and 10 of its 13 false negatives had failed in the training run and
passed in the later one (9 of those 10 use AWS; 12 of all 13 false negatives do). A
pipeline improvement invalidates the gate precisely in the direction of the improvement. On
the three frozen-configuration runs its AUC is 0.78, 0.70 and 0.74, against 0.76
cross-validated on the run it was fitted to.
This is the most interesting negative result in the project and deserves its own
subsection.

**D2. Label reliability is a hard ceiling on prediction, and it can be measured.** Three
runs of one configuration agree on 84% of labels (Fleiss kappa 0.68). Predicting one
replicate's labels from the mean of the other two reaches AUC 0.88 to 0.91, roughly the
ceiling for any per-function score. Retrained on the three runs, grouped cross-validation
gives 0.64 for the logistic regression and 0.76 for the random forest. The model fitted on
the earlier configuration reaches 0.70 to 0.78 on these runs, but that is not held out on
features, and simply reusing its own training labels reaches 0.69 to 0.76. No amount of
modelling exceeds the reliability of the labels.

**D3. Getting better concentrates waste rather than removing it.** Total spend fell 16%
while the failed-attempt share rose from 68.5% to 75.5%: cheaper per success, no better at
avoiding hopeless attempts. The replicates hold the share at 75.5%, 81.8% and 75.9%. That
is the argument for candidate screening, made from the data rather than from principle.

**D4. Two failure regimes need two remedies.** A, B and C lose more functions to
behavioral divergence than to compilation (B runs 96% buildable, 72% validated, 56%
tested), while D+ mostly fails before it runs (45%, 20%, 15%). B and C reproduce on every
replicate; A sits at parity (4 to 5 functions lost to compilation, 5 after it), and D+
fails to compile for 11, 14 and 8 of 20, so "mostly" holds on two of the three runs.
Repair prompts address the first regime; nothing in the current design addresses the
second.

**D5. How far can deterministic repair be pushed?** Package clause insertion and import
resolution are free and remove a whole class of build failures. Every further mechanical
repair displaces a language model call, which is the cheapest energy saving available
inside the pipeline.

**D6. Is the amortization horizon realistic?** A median N* between 1.3e5 and 1.0e6
invocations (across the replicates) is the honest framing of whether any of this matters, and it should be compared against plausible
serverless function lifetimes rather than left standing as a number.

**D7. Where the accounting boundary sits.** Inference energy is modeled at the provider
while host energy is measured locally. Roughly 90% of a job's wall clock (89% to 94% on the
replicates) is spent waiting on the API, with the host package averaging about 1 W against a
measured idle of 0.4 W, so gross host energy is 1.5 to 1.7 times the marginal figure.
Provider-side measurement would change the entire accounting and is the most valuable
piece of missing infrastructure.

## Arguments against the threats, and what the thesis established anyway

The purpose of this section is not to explain the threats away. It is to state, for each
one, how far it actually reaches, which conclusions survive it untouched, and which
require the qualification. An examiner is more persuaded by a precise boundary than by a
long list of caveats.

### The energy model reaches less far than it appears to (C1, N5)

- **Every count-based finding is independent of it.** The failed-attempt share, the
  buildable/validated/tested funnel, the run-to-run label churn, the AWS versus non-AWS
  asymmetry and the near-duplicate audit are counts and rank statistics. Not one of them
  changes if every energy coefficient is wrong.
- **The break-even threshold is a ratio.** N* is translation cost divided by
  per-invocation saving, so a scaling error in the inference coefficient moves every
  function's N* by the same factor. The ordering of functions, the shape of the
  distribution and its spread across four and a half orders of magnitude all survive; only
  the absolute position of the threshold moves.
- **The unmeasurable parameter is swept, not assumed.** GWDG confirmed the hardware and
  the FP8 precision and declined only to disclose concurrency, which the sensitivity
  analysis varies by a factor of sixteen, alongside node power and peak throughput.
- **The half of the accounting that could be measured, was.** Host energy is RAPL-measured
  on bare metal, per job and per attempt, and a host without counters is reported as
  unmetered rather than as zero.
- **The alternative is no number at all.** Provider-side inference energy is not observable
  from outside, and a documented model with a published sensitivity range is the strongest
  position available to any external study.

### The validation is lenient in a direction that protects the main claim (C2, C3, C4)

- **The bias is known and it is conservative where it matters.** Tolerant comparison
  inflates the measured success rate, so the reported rate is an upper bound. The central
  finding is that three quarters of the energy goes to attempts that produce nothing; a
  bias that inflates success makes that finding conservative rather than optimistic.
- **The threat applies asymmetrically, and saying so is stronger than a blanket caveat.**
  It weakens any claim of the form "the pipeline translates correctly" and strengthens any
  claim of the form "translation is expensive and often fails".
- **The larger threat was removed by design.** Expected outputs are recorded from the
  original rather than predicted by a model, and 45.9% of accepted tests carry a recorded
  output that differs from what the model proposed. Without that rule almost half the suite
  would assert a wrong result and would fail a correct translation.
- **The instrument makes its own strength visible.** Recording a failure kind and a
  comparison mode per case is what turns "50% success" into a three-level funnel. A weaker
  criterion is not hidden inside one number; it is legible in the report.
- **The emulator is a shared condition, not a differential one.** The Python original and
  the Go translation are executed against the same emulator, under the same environment
  helper, so a divergence between emulator and provider cannot manufacture a difference
  between the two sides.

### The instability is measured rather than assumed, which is itself the contribution (I1, D2)

- Single-run labelling is the norm in this literature and the instability is normally left
  unquantified. Measuring it, and reporting that three runs of the same frozen pipeline over
  the same corpus agree pairwise on only 82% to 85% of functions, is a result rather than a
  confession.
- **Both numbers now exist, and they should be kept apart.** Agreement within one
  configuration is 84% on average (Fleiss kappa 0.68, three runs); agreement across the
  configuration change is 76% (kappa 0.515, two runs). The second is the lower bound the
  earlier text anticipated; the first replaces "unmeasured". Quote kappa 0.68 as the
  within-configuration reliability, not 0.515.
- The consequence for prediction holds either way: a classifier fitted to labels that are
  not perfectly reproducible cannot be evaluated as though they were, and no published work
  in this area currently accounts for that.

### The bundled configuration change is traceable at the mechanism level (I2, I3)

- The net gain of five functions is not the only evidence. The per-stage counts show
  *which* functions moved and *how*: ten functions that previously never compiled now reach
  the test stage, nine of them cloud-using, and ten that previously reached it no longer
  compile, five of them from the most complex bucket. That is mechanism-level attribution,
  not a black-box delta.
- The ablation was not skipped by oversight but by cost: one full run is 5.5 to 8.5 hours
  and 1.2 to 1.4 MJ of facility energy, and the replicate series shows that one run per arm
  could not resolve a five-function effect anyway. Stating the price makes the omission a
  resource decision a reader can evaluate.
- The tuning direction was conservative. Every proposed tightening of a retry budget or a
  stagnation threshold was rejected because replay showed it costing completed functions,
  so the thresholds were fitted to avoid harm rather than to maximise a reported number.
  They were then exercised, unchanged, on three later runs they had not been tuned on.

### The corpus is small, but it is the largest defended one in this setting (E1, E3)

- The comparison point is 14 hand-picked functions from introductory tasks, documentation
  examples and self-written code. This corpus has 95 functions scraped from a public code
  corpus under stated inclusion criteria, stratified across four complexity buckets, each
  carrying validated tests, 392 in total. Building it was named as future work by the paper
  this thesis builds on.
- **86 is a defended number, not a claimed one.** Most benchmarks report a corpus size
  without checking for near-duplicates. Here the check was performed, and it found that
  four of seven duplicate groups cross repository boundaries, so the conventional defence
  of grouping by repository would have missed the most similar pairs entirely. Reporting 86
  instead of 95 costs nine rows and buys a defensible splitting protocol.
- **Contamination biases against the thesis, not for it.** If the model has seen these
  functions during pretraining, that inflates translation success. Observing that half the
  attempts still fail, under conditions favourable to the model, strengthens rather than
  weakens the conclusion that automated translation is expensive and unreliable at this
  scale.
- Small is a problem for the learned model and is stated as such. It is not a problem for
  the descriptive results, which are counts over the whole corpus.

### The spread in the amortisation threshold is a result, not noise (N1, N2, D6)

- A single break-even number would have been the less useful outcome. The finding that
  identical pipeline effort produces payback points four and a half orders of magnitude
  apart is what makes candidate selection a research question at all: if every function
  amortised at the same point, screening would be pointless by construction.
- The decision the number supports is per function anyway. A platform operator deciding
  whether to translate one function needs that function's threshold, not the corpus median,
  and the per-function values are reported.
- The spread also replicates the prior work, which reported a comparable range from a few
  thousand invocations to about a million on a different corpus and a different pipeline.

### What the thesis establishes that was not previously known

Claims that survive the threats above, in descending order of confidence.

1. **A labelled, test-validated, complexity-stratified corpus of serverless functions for
   translation research**, with an ex-ante feature vector recorded at run time for every
   attempt, answering an explicitly named open problem in the prior work.
2. **Side-effect-aware validation applied at corpus scale.** Translation correctness is
   asserted against emulated cloud state rather than against return values alone, which the
   prior work named as its first future challenge.
3. **Run-to-run label instability in LLM code translation is quantified**, apparently for
   the first time in this setting: three runs of one frozen configuration agree on 84% of
   functions (Fleiss kappa 0.68), and that bounds what any predictor fitted to single-run
   labels can achieve.
4. **Ex-ante prediction of translation success carries real but modest signal.** On the
   replicate series a grouped, leakage-audited cross-validation gives AUC 0.64 for the
   logistic regression (permutation p = 0.04) and 0.76 for the random forest, against a
   replicate ceiling of 0.89, and a single complexity threshold already captures most of
   the achievable energy saving. Which features carry it depends on the pipeline version: on 20260831-190900 the complexity bucket showed
   no effect (A vs D+ p = 0.37) and the fitted coefficients favour externally observable
   behaviour, but on the frozen configuration D+ succeeds 13% of the time against A's 63%
   (per-function Mann-Whitney p = 0.0002). Do not present "complexity is uninformative" as a
   property of the corpus.
5. **A gate learns a pipeline version, not translatability.** Improving the pipeline
   invalidates the gate in precisely the direction of the improvement. This is a general
   result about the predict-then-translate architecture, not an artefact of this corpus.
6. **The energy benefit of Python to Go translation is confined to a subclass.** For this
   corpus the saving lives entirely in SDK-heavy functions and is statistically
   indistinguishable from zero elsewhere, which is more actionable than a corpus-wide
   average would have been.
7. **Improving the pipeline concentrates waste rather than removing it.** Cost per success
   fell by a quarter while the share of energy spent on failed attempts rose, which is an
   argument for screening made from measurement rather than from principle.
8. **The cheap rule does not work.** The obvious hand-written screen, a blocklist of
   dependencies without a target-language equivalent, cannot decline a single function of
   this corpus, which is why a learned method was required rather than preferred.

## Defence notes for the presentation

Four threats, each with an opening concession, the bound that limits it, and the turn.
Concede first: an examiner who hears the concession stops looking for it.

### "Tied to one model and one pipeline configuration"

- **Concede:** every number is produced by one model at one temperature, in one
  configuration of the graph.
- **Bound:** fixing the model is not incidental, it is what makes the energy comparison
  possible at all. The energy coefficients are model-specific, so varying the model would
  confound every joule reported. Holding it fixed is the control, not the limitation.
- **Turn:** the pipeline is configuration-driven. The model, the language pair and the
  whole stage graph arrive in one request body, so reproducing this study for another model
  is a configuration change rather than a reimplementation. And the one thing that was
  varied, the configuration, between two full runs, produced the most interesting result in
  the thesis: that the prediction gate learns a pipeline version rather than
  translatability.
- **If pressed on generalisation:** the descriptive findings are about *this* pipeline and
  are stated that way throughout. The architectural finding in point 5 is the one that
  generalises, and it generalises because it is about the relationship between a gate and a
  pipeline, not about either one specifically.

### "Inference energy is modeled, not measured"

- **Concede:** the largest term in the accounting is a model. The host term is measured;
  the inference term is not.
- **Bound:** it cannot be measured from outside the provider, so the alternative to a
  documented model is no number at all. The model is built on hardware the provider
  confirmed, and the single parameter they declined to disclose is swept across a factor of
  sixteen, alongside node power and throughput.
- **Turn, and this is the strong one:** almost nothing in the thesis depends on it. The
  waste share, the funnel, the label churn, the AWS asymmetry and the leakage audit are
  counts. The break-even threshold is a ratio, so a scaling error moves every function's
  threshold by the same factor, and the ordering and the four-and-a-half-order-of-magnitude
  spread survive untouched. What moves is the absolute position of a line, and the
  sensitivity analysis reports how far.
- **If pressed:** name the one conclusion that is genuinely at risk, which is the width of
  the window in which the gate is worthwhile. Say so before it is asked.

### "Only 86 truly independent evaluation functions"

- **Concede:** 86 is small, and it is too small for confident claims about a learned model.
- **Bound:** it is small relative to machine-learning practice, not relative to this field.
  The work this thesis builds on used 14 hand-picked functions. 95 scraped functions under
  stated inclusion criteria, stratified across four complexity buckets, with 392 validated
  tests, is what that paper named as future work.
- **Turn:** 86 is a defended number rather than a claimed one. Most benchmarks report a
  corpus size without ever checking for near-duplicates. This one checked, and found that
  four of seven duplicate groups cross repository boundaries, so the conventional defence of
  grouping by repository would have missed the most similar pairs. Two functions are
  structurally identical and share nothing in their metadata. Reporting 86 costs nine rows
  and buys a splitting protocol that can be defended.
- **If pressed:** distinguish the claims. Small hurts the model. It does not hurt the
  funnel, the energy asymmetry or the waste share, which are counts over the full corpus,
  and it does not hurt the architectural finding about the gate.

### "The amortisation threshold differs largely within the corpus"

- **Concede:** break-even points span more than four orders of magnitude, so the median is
  a weak summary and is reported alongside the distribution rather than instead of it.
- **Bound:** the spread is not measurement noise. It is a real property of the corpus, and
  it replicates the range reported by the prior work on a different corpus and a different
  pipeline.
- **Turn:** the spread is the result. If every function amortised at the same point,
  candidate selection would be pointless by construction; it is a research question
  precisely because the payback varies this much. The decision the number supports is a
  per-function decision anyway, so a platform operator needs that function's threshold, not
  the corpus median.
- **If pressed on whether any of it repays:** be direct. A third of the validated
  translations are not faster than Python at all and never repay, and that is reported
  rather than excluded. The honest framing is that translation pays back for a
  identifiable subclass of functions, which is what makes screening the interesting part of
  the problem.

### One answer worth preparing for any of the four

If asked what would change the conclusion: provider-side energy measurement, repeated runs
of the same configuration, and a second corpus in a different language pair. Naming the
three experiments that would settle it is a stronger close than defending the ones that
were run.
