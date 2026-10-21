"""Robustness analysis for docs/alternatives-analysis/airtable-cache.md.

Run: python3 docs/alternatives-analysis/airtable-cache-robustness.py  (needs numpy)
"""
import numpy as np

SEED = 20261021
TRIALS = 10_000
BAND = 0.20          # +/- relative perturbation for the uniform-band method
DIRICHLET_K = 100    # concentration: alpha = K * base_weights

CRITERIA = ["C1", "C2", "C3", "C4", "C5", "C6", "C7", "C8", "C9"]
W = np.array([0.20, 0.20, 0.10, 0.10, 0.10, 0.10, 0.05, 0.10, 0.05])
ALTS = ["Alt 1", "Alt 2", "Alt 3", "Alt 4"]
S = np.array([
    [5, 5, 5, 3, 4, 4, 5, 4, 4],  # Alt 1: Firestore + webhook
    [4, 4, 5, 4, 5, 3, 5, 5, 5],  # Alt 2: In-memory, 1 instance
    [5, 5, 5, 3, 3, 4, 3, 3, 4],  # Alt 3: R2 from Cloud Run
    [5, 5, 4, 2, 4, 4, 4, 2, 2],  # Alt 4: Worker + R2
], dtype=float)
S_MIN, S_MAX = 1, 5

assert abs(W.sum() - 1) < 1e-9
V = S @ W
order = np.argsort(-V)
top, second = order[0], order[1]
print("Totals:", {a: round(v, 4) for a, v in zip(ALTS, V)})

# 1. Dominance
for i, a in enumerate(ALTS):
    if all((S[i] >= S[j]).all() for j in range(len(ALTS)) if j != i):
        print(f"Dominance: {a} dominates")
        break
else:
    print("Dominance: none")

# 2. Winning margin
delta = V[top] - V[second]
delta_bar = delta / (S_MAX - S_MIN)
print(f"Margin: {ALTS[top]} over {ALTS[second]}  delta={delta:.4f}  "
      f"delta_bar={delta_bar:.4f}  close={delta_bar < 0.05}")

# 3. Critical weight (flip point), other weights renormalised proportionally
print("Flip points (criterion, W_k, d_k, delta_k, |delta_k|/W_k):")
for k, c in enumerate(CRITERIA):
    d = S[top, k] - S[second, k]
    if d == 0:  # moving this weight only rescales the others; it can never reverse the order
        print(f"  {c}  W={W[k]:.2f}  d={d:+.0f}  no flip")
        continue
    denom = (delta - W[k] * d) / (1 - W[k]) - d
    dk = delta / denom
    feasible = 0 <= W[k] + dk <= 1
    print(f"  {c}  W={W[k]:.2f}  d={d:+.0f}  delta_k={dk:+.4f}  "
          f"rel={abs(dk) / W[k]:.2f}  {'ok' if feasible else 'infeasible'}")

# 4. Monte Carlo
rng = np.random.default_rng(SEED)

def win_probs(weights):
    totals = weights @ S.T
    winners = totals.argmax(axis=1)
    return {a: (winners == i).mean() for i, a in enumerate(ALTS)}

band = W * rng.uniform(1 - BAND, 1 + BAND, size=(TRIALS, len(W)))
band /= band.sum(axis=1, keepdims=True)
print(f"Monte Carlo, +/-{BAND:.0%} band, {TRIALS} trials, seed {SEED}:", win_probs(band))

dirichlet = rng.dirichlet(DIRICHLET_K * W, size=TRIALS)
print(f"Monte Carlo, Dirichlet(alpha={DIRICHLET_K}*W), {TRIALS} trials, seed {SEED}:",
      win_probs(dirichlet))
