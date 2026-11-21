#!/usr/bin/env python3
"""
Offline pure-Python trainer for rf-heatmap's 12 -> 24 (ReLU) -> 5 (Softmax) MLP.
Zero external dependencies (Python 3 stdlib only).
Generates rf-heatmap/internal/rfml/model_weights.go with trained float32 weights.
"""

import math
import os
import random

CLASSES = [
    "Fixed OOK",
    "Rolling / KeyFob",
    "Sensor / Weather",
    "Continuous / Jammer",
    "Noise",
]

IN_DIM = 12
HIDDEN_DIM = 24
OUT_DIM = 5


def clamp(v: float, lo: float = 0.0, hi: float = 1.0) -> float:
    return lo if v < lo else (hi if v > hi else v)


def gen_sample(cls: int, rng: random.Random) -> list[float]:
    """Generate a realistic 12-feature vector matching internal/rfml/features.go."""
    if cls == 0:  # Fixed OOK (Princeton 24b, CAME 12/24b, Linear 10b: 20..56 pulses/frame)
        band = rng.choice([0.25, 0.50, 0.50, 0.50, 0.75])
        reps = rng.randint(2, 6)
        frame_len = rng.uniform(20, 56)
        pulses = int(frame_len * reps)
        short_us = rng.uniform(220, 480)
        ratio = rng.uniform(1.9, 3.4)
        long_us = short_us * ratio
        sync_us = rng.uniform(4500, 15000)
        jitter = rng.uniform(0.01, 0.16)
        duty = rng.uniform(0.30, 0.58)
        snr = rng.uniform(14, 48)
        glitch_rate = rng.uniform(0.0, 0.06)
    elif cls == 1:  # Rolling / KeyFob (KeeLoq 66b + preamble: 110..180 pulses/frame)
        band = rng.choice([0.25, 0.50, 0.50, 0.75, 0.75])
        reps = rng.randint(1, 5)
        frame_len = rng.uniform(110, 180)
        pulses = int(frame_len * reps)
        short_us = rng.uniform(180, 460)
        ratio = rng.uniform(1.7, 2.6)
        long_us = short_us * ratio
        sync_us = rng.uniform(3500, 18000)
        jitter = rng.uniform(0.02, 0.18)
        duty = rng.uniform(0.36, 0.62)
        snr = rng.uniform(15, 48)
        glitch_rate = rng.uniform(0.0, 0.07)
    elif cls == 2:  # Sensor / Weather (periodic low-bitrate OOK/FSK telemetry)
        band = rng.choice([0.50, 0.50, 0.75, 1.0])
        reps = rng.randint(1, 3)
        frame_len = rng.uniform(32, 96)
        pulses = int(frame_len * reps)
        short_us = rng.uniform(520, 1400)
        ratio = rng.uniform(1.6, 4.0)
        long_us = min(3000.0, short_us * ratio)
        sync_us = rng.uniform(3800, 25000)
        jitter = rng.uniform(0.02, 0.20)
        duty = rng.uniform(0.12, 0.36)  # Short carrier pulses, long gaps
        snr = rng.uniform(10, 38)
        glitch_rate = rng.uniform(0.0, 0.08)
    elif cls == 3:  # Continuous / Jammer (CW carrier or high-duty-cycle interference)
        band = rng.choice([0.25, 0.50, 0.75, 1.0])
        reps = rng.choice([0, 0, 1])
        pulses = rng.randint(6, 90)
        frame_len = float(pulses)
        short_us = rng.uniform(400, 1500)
        long_us = rng.uniform(short_us, 3000)
        ratio = long_us / max(short_us, 1.0)
        sync_us = rng.choice([0.0, 0.0, 0.0, rng.uniform(0, 1500)])
        jitter = rng.uniform(0.0, 0.25)
        duty = rng.uniform(0.74, 0.99)  # Dominant carrier-on duty cycle
        snr = rng.uniform(18, 50)
        glitch_rate = rng.uniform(0.0, 0.10)
    else:  # cls == 4: Noise (atmospheric / EMI spikes)
        band = rng.choice([0.25, 0.50, 0.75, 1.0])
        reps = rng.choice([0, 0, 1])
        pulses = rng.randint(6, 400)
        frame_len = float(pulses)
        short_us = rng.uniform(70, 800)
        long_us = rng.uniform(short_us * 1.1, 2800)
        ratio = long_us / max(short_us, 1.0)
        sync_us = rng.choice([0.0, 0.0, rng.uniform(0, 5000)])
        jitter = rng.uniform(0.30, 0.85)  # High unstructured jitter
        duty = rng.uniform(0.15, 0.72)
        snr = rng.uniform(0, 14)
        glitch_rate = rng.uniform(0.18, 0.75)

    return [
        band,
        clamp(math.log10(pulses + 1.0) / 3.0),
        clamp(short_us / 1500.0),
        clamp(long_us / 3000.0),
        clamp(ratio / 5.0),
        clamp(math.log10(sync_us + 1.0) / 5.0),
        clamp(jitter),
        clamp(duty),
        clamp(frame_len / 180.0),
        clamp(reps / 8.0),
        clamp(snr / 50.0),
        clamp(glitch_rate),
    ]


def train_mlp():
    rng = random.Random(20261121)

    train_data = [(gen_sample(c, rng), c) for c in range(OUT_DIM) for _ in range(800)]
    val_data = [(gen_sample(c, rng), c) for c in range(OUT_DIM) for _ in range(200)]

    # Xavier/He initialization.
    s1 = math.sqrt(2.0 / IN_DIM)
    s2 = math.sqrt(2.0 / HIDDEN_DIM)
    w1 = [[rng.gauss(0.0, s1) for _ in range(IN_DIM)] for _ in range(HIDDEN_DIM)]
    b1 = [0.0] * HIDDEN_DIM
    w2 = [[rng.gauss(0.0, s2) for _ in range(HIDDEN_DIM)] for _ in range(OUT_DIM)]
    b2 = [0.0] * OUT_DIM

    # Adam moments.
    mw1 = [[0.0] * IN_DIM for _ in range(HIDDEN_DIM)]
    vw1 = [[0.0] * IN_DIM for _ in range(HIDDEN_DIM)]
    mb1 = [0.0] * HIDDEN_DIM
    vb1 = [0.0] * HIDDEN_DIM
    mw2 = [[0.0] * HIDDEN_DIM for _ in range(OUT_DIM)]
    vw2 = [[0.0] * HIDDEN_DIM for _ in range(OUT_DIM)]
    mb2 = [0.0] * OUT_DIM
    vb2 = [0.0] * OUT_DIM

    lr = 0.015
    wd = 1e-4
    beta1, beta2, eps = 0.9, 0.999, 1e-8
    batch_size = 64
    epochs = 28
    step = 0

    for epoch in range(1, epochs + 1):
        rng.shuffle(train_data)
        for start in range(0, len(train_data), batch_size):
            batch = train_data[start : start + batch_size]
            bs = float(len(batch))
            gw1 = [[0.0] * IN_DIM for _ in range(HIDDEN_DIM)]
            gb1 = [0.0] * HIDDEN_DIM
            gw2 = [[0.0] * HIDDEN_DIM for _ in range(OUT_DIM)]
            gb2 = [0.0] * OUT_DIM

            for x, y in batch:
                h = [0.0] * HIDDEN_DIM
                for j in range(HIDDEN_DIM):
                    z = b1[j]
                    row = w1[j]
                    for i in range(IN_DIM):
                        z += row[i] * x[i]
                    h[j] = z if z > 0.0 else 0.0

                logits = [0.0] * OUT_DIM
                max_l = -1e30
                for k in range(OUT_DIM):
                    z = b2[k]
                    row = w2[k]
                    for j in range(HIDDEN_DIM):
                        z += row[j] * h[j]
                    logits[k] = z
                    if z > max_l:
                        max_l = z

                exps = [math.exp(l - max_l) for l in logits]
                s_exp = sum(exps)
                probs = [e / s_exp for e in exps]

                dlogits = probs[:]
                dlogits[y] -= 1.0

                for k in range(OUT_DIM):
                    dk = dlogits[k]
                    gb2[k] += dk
                    row_g = gw2[k]
                    for j in range(HIDDEN_DIM):
                        row_g[j] += dk * h[j]

                for j in range(HIDDEN_DIM):
                    if h[j] <= 0.0:
                        continue
                    dj = 0.0
                    for k in range(OUT_DIM):
                        dj += w2[k][j] * dlogits[k]
                    gb1[j] += dj
                    row_g = gw1[j]
                    for i in range(IN_DIM):
                        row_g[i] += dj * x[i]

            step += 1
            b1_corr = 1.0 - beta1**step
            b2_corr = 1.0 - beta2**step
            cur_lr = lr * (0.96 ** (epoch - 1))

            for k in range(OUT_DIM):
                g = gb2[k] / bs
                mb2[k] = beta1 * mb2[k] + (1.0 - beta1) * g
                vb2[k] = beta2 * vb2[k] + (1.0 - beta2) * g * g
                b2[k] -= cur_lr * (mb2[k] / b1_corr) / (math.sqrt(vb2[k] / b2_corr) + eps)
                for j in range(HIDDEN_DIM):
                    g = gw2[k][j] / bs + wd * w2[k][j]
                    mw2[k][j] = beta1 * mw2[k][j] + (1.0 - beta1) * g
                    vw2[k][j] = beta2 * vw2[k][j] + (1.0 - beta2) * g * g
                    w2[k][j] -= cur_lr * (mw2[k][j] / b1_corr) / (math.sqrt(vw2[k][j] / b2_corr) + eps)

            for j in range(HIDDEN_DIM):
                g = gb1[j] / bs
                mb1[j] = beta1 * mb1[j] + (1.0 - beta1) * g
                vb1[j] = beta2 * vb1[j] + (1.0 - beta2) * g * g
                b1[j] -= cur_lr * (mb1[j] / b1_corr) / (math.sqrt(vb1[j] / b2_corr) + eps)
                for i in range(IN_DIM):
                    g = gw1[j][i] / bs + wd * w1[j][i]
                    mw1[j][i] = beta1 * mw1[j][i] + (1.0 - beta1) * g
                    vw1[j][i] = beta2 * vw1[j][i] + (1.0 - beta2) * g * g
                    w1[j][i] -= cur_lr * (mw1[j][i] / b1_corr) / (math.sqrt(vw1[j][i] / b2_corr) + eps)

    # Evaluate on validation set.
    correct = 0
    per_class = [0] * OUT_DIM
    for x, y in val_data:
        h = [max(0.0, b1[j] + sum(w1[j][i] * x[i] for i in range(IN_DIM))) for j in range(HIDDEN_DIM)]
        logits = [b2[k] + sum(w2[k][j] * h[j] for j in range(HIDDEN_DIM)) for k in range(OUT_DIM)]
        pred = max(range(OUT_DIM), key=lambda k: logits[k])
        if pred == y:
            correct += 1
            per_class[y] += 1

    acc = 100.0 * correct / len(val_data)
    print(f"Validation accuracy: {acc:.2f}% ({correct}/{len(val_data)})")
    for c, name in enumerate(CLASSES):
        print(f"  {name:20s}: {per_class[c]}/200 ({per_class[c]/2.0:.1f}%)")
    return w1, b1, w2, b2


def write_go_weights(path: str, w1, b1, w2, b2):
    def fmt_vec(vec):
        return ", ".join(f"{v:.6f}" for v in vec)

    lines = [
        "// Code generated by scripts/train_rfml.py; DO NOT EDIT.",
        "",
        "package rfml",
        "",
        "const (",
        f"\tmlpInputDim  = {IN_DIM}",
        f"\tmlpHiddenDim = {HIDDEN_DIM}",
        f"\tmlpOutputDim = {OUT_DIM}",
        ")",
        "",
        "var mlpW1 = [mlpHiddenDim][mlpInputDim]float32{",
    ]
    for row in w1:
        lines.append(f"\t{{{fmt_vec(row)}}},")
    lines.append("}")
    lines.append("")
    lines.append(f"var mlpB1 = [mlpHiddenDim]float32{{{fmt_vec(b1)}}}")
    lines.append("")
    lines.append("var mlpW2 = [mlpOutputDim][mlpHiddenDim]float32{")
    for row in w2:
        lines.append(f"\t{{{fmt_vec(row)}}},")
    lines.append("}")
    lines.append("")
    lines.append(f"var mlpB2 = [mlpOutputDim]float32{{{fmt_vec(b2)}}}")
    lines.append("")

    with open(path, "w", encoding="utf-8") as f:
        f.write("\n".join(lines))
    print(f"Wrote {path} ({os.path.getsize(path)} bytes)")


if __name__ == "__main__":
    w1, b1, w2, b2 = train_mlp()
    root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    out_path = os.path.join(root, "internal", "rfml", "model_weights.go")
    write_go_weights(out_path, w1, b1, w2, b2)
