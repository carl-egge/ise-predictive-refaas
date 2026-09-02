#!/usr/bin/env python3
"""[I8] The predictor's own energy, in the units cmd/energy already uses.

Two terms, measured rather than asserted:

  * feature extraction -- `cmd/pyscan` over the artifact, which parses the Python
    through a real interpreter and is by far the dominant cost ([I3]);
  * inference -- scoring one 56-element vector with the exported M1 coefficients.

Both are wall-clock on CPU. They are costed at `hardware.node_power_watts` and
`facility.pue` from evaluation/energy.config.json -- i.e. at the power of the
*GPU inference node*, which no CPU-side scan comes close to drawing. That is a
deliberate upper bound: if the predictor is negligible even when charged the
LLM node's power, it is negligible.
"""
import argparse
import glob
import json
import os
import subprocess
import time

WH = 3600.0


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--pyscan-bin", required=True)
    ap.add_argument("--artifacts", required=True, help="glob for the artifact .zip files")
    ap.add_argument("--config", default="evaluation/energy.config.json")
    ap.add_argument("--energy-json", default="", help="cmd/energy -json, for E_translation")
    ap.add_argument("--repeats", type=int, default=3)
    args = ap.parse_args()

    cfg = json.load(open(args.config))
    watts = cfg["hardware"]["node_power_watts"]
    pue = cfg["facility"]["pue"]

    files = sorted(glob.glob(args.artifacts))
    if not files:
        raise SystemExit("no artifacts matched %r" % args.artifacts)

    # --- feature extraction, whole corpus, best of `repeats` (minimum, per [H6]) ---
    batch = []
    for _ in range(args.repeats):
        t0 = time.perf_counter()
        subprocess.run([args.pyscan_bin] + files, stdout=subprocess.DEVNULL,
                       stderr=subprocess.DEVNULL, check=True)
        batch.append(time.perf_counter() - t0)
    batch_s = min(batch)

    # --- feature extraction, single artifact: what the service actually pays ---
    single = []
    for _ in range(args.repeats):
        t0 = time.perf_counter()
        subprocess.run([args.pyscan_bin, files[0]], stdout=subprocess.DEVNULL,
                       stderr=subprocess.DEVNULL, check=True)
        single.append(time.perf_counter() - t0)
    single_s = min(single)

    # --- inference: a dot product over the standardized feature vector ---
    import numpy as np
    rng = np.random.default_rng(0)
    x = rng.normal(size=56)
    w = rng.normal(size=56)
    reps = 200000
    t0 = time.perf_counter()
    for _ in range(reps):
        1.0 / (1.0 + np.exp(-(float(x @ w) + 0.1)))
    infer_s = (time.perf_counter() - t0) / reps

    def joules(seconds):
        return seconds * watts * pue

    per_fn_s = batch_s / len(files)
    print("constants: node_power_watts=%g, pue=%g (charged at the GPU node's power, "
          "an upper bound for CPU work)" % (watts, pue))
    print("artifacts: %d\n" % len(files))
    print("feature extraction, whole corpus : %.2f s  -> %.1f J  (%.3g Wh)"
          % (batch_s, joules(batch_s), joules(batch_s) / WH))
    print("feature extraction, per function : %.3f s -> %.1f J" % (per_fn_s, joules(per_fn_s)))
    print("feature extraction, cold single  : %.3f s -> %.1f J  (includes process start)"
          % (single_s, joules(single_s)))
    print("M1 inference, per function       : %.3g s -> %.3g J" % (infer_s, joules(infer_s)))

    total_s = per_fn_s + infer_s
    print("\npredictor total, per function    : %.3f s -> %.1f J" % (total_s, joules(total_s)))

    if args.energy_json and os.path.exists(args.energy_json):
        d = json.load(open(args.energy_json))
        rows = list(d["translations"]) + list(
            d.get("failed_attempts", {}).get("translations", []))
        e = [t["facility_joules"] for t in rows]
        mean_e = sum(e) / len(e)
        med_e = sorted(e)[len(e) // 2]
        p = joules(total_s)
        print("\nagainst the measured translation cost of the same corpus:")
        print("  mean E_translation   %.0f J   -> E_predictor / E_translation = %.2e"
              % (mean_e, p / mean_e))
        print("  median E_translation %.0f J   -> E_predictor / E_translation = %.2e"
              % (med_e, p / med_e))
        print("  break-even: the predictor pays for itself if it avoids one wasted")
        print("              translation per %.0f functions screened (mean cost)."
              % (mean_e / p))


if __name__ == "__main__":
    main()
