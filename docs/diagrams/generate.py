#!/usr/bin/env python3
"""Build the diagrams in docs/diagrams/.

Each scene is laid out once and written twice: an .svg that renders inline in
the docs, and an .excalidraw beside it so the drawing stays editable.

    python3 docs/diagrams/generate.py        # from the repo root
"""

import html
import json
import os

INK = "#1e1e1e"
RED = "#c92a2a"
BLUE = "#1971c2"
GREY = "#868e96"
AMBER = "#e67700"
GREEN = "#2f9e44"
VIOLET = "#6741d9"

F_RED = "#ffe3e3"
F_BLUE = "#d0ebff"
F_GREY = "#f1f3f5"
F_AMBER = "#fff3bf"
F_GREEN = "#d3f9d8"
F_VIOLET = "#e5dbff"

HAND = 1
CODE = 3

SANS = "ui-sans-serif, -apple-system, 'Segoe UI', Helvetica, Arial, sans-serif"
MONO = "ui-monospace, 'Cascadia Code', Menlo, Consolas, monospace"

OUT = os.environ.get("OUT", "docs/diagrams")

_seq = [0]


def _uid(p):
    _seq[0] += 1
    return f"{p}{_seq[0]:04d}"


def _base(kind, x, y, w, h, stroke=INK, bg="transparent", style="solid", width=2):
    return {
        "id": _uid(kind[:2]), "type": kind,
        "x": x, "y": y, "width": w, "height": h, "angle": 0,
        "strokeColor": stroke, "backgroundColor": bg,
        "fillStyle": "solid", "strokeWidth": width, "strokeStyle": style,
        "roughness": 1, "opacity": 100, "groupIds": [], "frameId": None,
        "roundness": {"type": 3} if kind == "rectangle" else None,
        "seed": _seq[0] * 7919, "version": 1, "versionNonce": _seq[0] * 104729,
        "isDeleted": False, "boundElements": [], "updated": 1,
        "link": None, "locked": False,
    }


def text(x, y, s, size=15, colour=INK, font=HAND, weight="normal"):
    lines = s.split("\n")
    el = _base("text", x, y, max(len(l) for l in lines) * size * 0.56,
               len(lines) * size * 1.35)
    el.update({
        "text": s, "originalText": s, "fontSize": size, "fontFamily": font,
        "textAlign": "left", "verticalAlign": "top", "containerId": None,
        "lineHeight": 1.35, "strokeColor": colour, "autoResize": True,
        "_weight": weight,
    })
    return el


def box(x, y, w, h, label, stroke=INK, bg="transparent", size=15, font=HAND,
        style="solid"):
    rect = _base("rectangle", x, y, w, h, stroke=stroke, bg=bg, style=style)
    lines = label.split("\n")
    th = len(lines) * size * 1.35
    t = _base("text", x + 8, y + (h - th) / 2, w - 16, th)
    t.update({
        "text": label, "originalText": label, "fontSize": size,
        "fontFamily": font, "textAlign": "center", "verticalAlign": "middle",
        "containerId": rect["id"], "lineHeight": 1.35, "strokeColor": stroke,
        "autoResize": False, "_cx": x + w / 2, "_cy": y + h / 2,
        "_weight": "normal",
    })
    rect["boundElements"] = [{"id": t["id"], "type": "text"}]
    return [rect, t]


def line(pts, colour=INK, style="solid", head="arrow", width=2):
    xs = [p[0] for p in pts]
    ys = [p[1] for p in pts]
    el = _base("arrow", pts[0][0], pts[0][1], max(xs) - min(xs),
               max(ys) - min(ys), stroke=colour, style=style, width=width)
    el.update({
        "points": [[p[0] - pts[0][0], p[1] - pts[0][1]] for p in pts],
        "lastCommittedPoint": None, "startBinding": None, "endBinding": None,
        "startArrowhead": None, "endArrowhead": head, "elbowed": False,
        "_abs": pts,
    })
    return el


def arrow(x1, y1, x2, y2, **kw):
    return line([(x1, y1), (x2, y2)], **kw)


def rule(x1, x2, y, colour="#dee2e6"):
    return line([(x1, y), (x2, y)], colour=colour, head=None, width=2)


# ------------------------------------------------------------------ render --

def _svg(elements, title):
    xs, ys = [], []
    for el in elements:
        if el["type"] == "arrow":
            for px, py in el["_abs"]:
                xs.append(px)
                ys.append(py)
        else:
            xs += [el["x"], el["x"] + el["width"]]
            ys += [el["y"], el["y"] + el["height"]]
    pad = 36
    x0, y0 = min(xs) - pad, min(ys) - pad
    w, h = max(xs) - x0 + pad, max(ys) - y0 + pad

    out = [
        f'<svg xmlns="http://www.w3.org/2000/svg" '
        f'viewBox="{x0:.0f} {y0:.0f} {w:.0f} {h:.0f}" '
        f'width="{w:.0f}" height="{h:.0f}" font-family="{SANS}" '
        f'role="img" aria-label="{html.escape(title)}">',
        f"<title>{html.escape(title)}</title>",
        "<defs>",
    ]
    heads = sorted({el["strokeColor"] for el in elements
                    if el["type"] == "arrow" and el["endArrowhead"]})
    for i, col in enumerate(heads):
        out.append(
            f'<marker id="h{i}" viewBox="0 0 10 10" refX="9" refY="5" '
            f'markerWidth="6" markerHeight="6" orient="auto-start-reverse">'
            f'<path d="M 0 0 L 10 5 L 0 10 z" fill="{col}"/></marker>')
    out.append("</defs>")
    out.append(f'<rect x="{x0:.0f}" y="{y0:.0f}" width="{w:.0f}" '
               f'height="{h:.0f}" fill="#ffffff"/>')

    for el in elements:
        kind = el["type"]
        if kind == "rectangle":
            dash = ' stroke-dasharray="8 5"' if el["strokeStyle"] == "dashed" else ""
            fill = el["backgroundColor"]
            fill = "none" if fill == "transparent" else fill
            out.append(
                f'<rect x="{el["x"]:.1f}" y="{el["y"]:.1f}" '
                f'width="{el["width"]:.1f}" height="{el["height"]:.1f}" rx="8" '
                f'fill="{fill}" stroke="{el["strokeColor"]}" '
                f'stroke-width="{el["strokeWidth"]}"{dash}/>')
        elif kind == "arrow":
            pts = " ".join(f"{px:.1f},{py:.1f}" for px, py in el["_abs"])
            dash = ' stroke-dasharray="7 5"' if el["strokeStyle"] == "dashed" else ""
            mk = ""
            if el["endArrowhead"]:
                mk = f' marker-end="url(#h{heads.index(el["strokeColor"])})"'
            out.append(
                f'<polyline points="{pts}" fill="none" '
                f'stroke="{el["strokeColor"]}" '
                f'stroke-width="{el["strokeWidth"]}" stroke-linecap="round" '
                f'stroke-linejoin="round"{dash}{mk}/>')
        elif kind == "text":
            size = el["fontSize"]
            fam = MONO if el["fontFamily"] == CODE else SANS
            wt = el.get("_weight", "normal")
            lines = el["text"].split("\n")
            lh = size * 1.35
            if el["containerId"]:
                cx = el["_cx"]
                top = el["_cy"] - (len(lines) * lh) / 2 + lh * 0.76
                anchor, xpos = "middle", cx
            else:
                top = el["y"] + lh * 0.76
                anchor, xpos = "start", el["x"]
            for i, ln in enumerate(lines):
                out.append(
                    f'<text xml:space="preserve" x="{xpos:.1f}" '
                    f'y="{top + i * lh:.1f}" text-anchor="{anchor}" '
                    f'font-size="{size}" font-family="{fam}" '
                    f'font-weight="{wt}" fill="{el["strokeColor"]}">'
                    f"{html.escape(ln)}</text>")
    out.append("</svg>")
    return "\n".join(out)


def scene(elements, name, title):
    os.makedirs(OUT, exist_ok=True)
    with open(f"{OUT}/{name}.svg", "w") as fh:
        fh.write(_svg(elements, title))
    clean = [{k: v for k, v in el.items() if not k.startswith("_")}
             for el in elements]
    with open(f"{OUT}/{name}.excalidraw", "w") as fh:
        json.dump({"type": "excalidraw", "version": 2, "source": "daiquiri/docs",
                   "elements": clean,
                   "appState": {"gridSize": None,
                                "viewBackgroundColor": "#ffffff"},
                   "files": {}}, fh, indent=2)
    print(f"{OUT}/{name}.svg + .excalidraw   ({len(elements)} elements)")


# ---------------------------------------------------------------- pipeline --

def pipeline():
    e = [text(40, 20, "The pipeline", size=26, weight="bold"),
         text(40, 60, "One direction. Each stage narrows the data and keeps "
                      "what it dropped.", size=14, colour=GREY)]

    # (stage label, stroke, fill, note on the right, what leaves this stage)
    stages = [
        ("16.5 MB JSONL capture", GREY, F_GREY,
         "one OTel log record per line",
         "20,000 lines"),
        ("otel\nstreaming wire decode", INK, "transparent",
         "a malformed line is counted and\nskipped, never fatal",
         "20,000 events"),
        ("classify\ntable-driven taxonomy", INK, "transparent",
         "adding a K8s reason is adding a row,\nwith no switch to edit",
         "19,817 noise · 183 issues"),
        ("group\ncoalesce on the key", INK, "transparent",
         "(Kind, Workload, Namespace,\n Reason, Rule)",
         "3 - 10 findings"),
        ("link\nparent-array forest", VIOLET, F_VIOLET,
         "one int per finding, and time\ncan only veto an edge",
         "+ one parent int each"),
        ("diagnose\npattern, cadence, signature", INK, "transparent",
         "shape over time, plus what the\nrecord bodies prove",
         "incidents, worst first"),
        ("report\ntable / tree / json", INK, "transparent",
         "colour is emphasis only. strip it\nand the bytes match.",
         None),
    ]

    x, w, h, gap = 150, 330, 78, 128
    y = 116
    for i, (label, stroke, bg, note, out) in enumerate(stages):
        e += box(x, y, w, h, label, stroke=stroke, bg=bg, size=15)
        e.append(text(x + w + 44, y + 20, note, size=13, colour=GREY))
        if out:
            e.append(arrow(x + w / 2, y + h + 2, x + w / 2, y + gap - 10))
            e.append(text(x + w / 2 + 18, y + h + 12, out, size=12,
                          colour=BLUE, font=CODE))
        y += gap
    e.append(arrow(x + w / 2, y - gap + h + 2, x + w / 2, y - gap + h + 40))
    e.append(text(x + w / 2 + 18, y - gap + h + 12, "stdout", size=12,
                  colour=BLUE, font=CODE))

    # The callout sits in its own column, clear of the note text.
    cx, cy = 790, 366
    e += box(cx, cy, 320, 100,
             "the 19,817 stay in memory\n\nlifecycle events are the evidence\n"
             "a trail gets built from", stroke=GREEN, bg=F_GREEN, size=13)
    e.append(line([(cx - 8, cy + 50), (cx - 48, cy + 50)], colour=GREEN,
                  style="dashed", head=None))
    e.append(text(150, y - gap + h + 70,
                  "~140 ms end to end. The brief allows 5 seconds.",
                  size=15, colour=GREEN))
    scene(e, "pipeline", "daiquiri pipeline")


# ------------------------------------------------------------ forest shapes --

def forest_shapes():
    e = [text(40, 20, "Two shapes the forest makes", size=26, weight="bold"),
         text(40, 60, "Every finding has at most one parent, so a walk upward "
                      "always terminates and never branches.",
              size=14, colour=GREY)]

    # ---- left: 05-test-b, one cause and six symptoms ----------------------
    e.append(text(40, 122, "05-test-b   one cause, six symptoms", size=18,
                  weight="bold"))
    e += box(180, 156, 350, 58, "node-4  NodeHasDiskPressure\n10:15:00.000",
             stroke=RED, bg=F_RED, size=14)
    e.append(text(546, 176, "ROOT", size=13, colour=RED, font=CODE))

    kids = [("auth-service", "production", "+1m25.4s"),
            ("payment-service", "production", "+15.4s"),
            ("checkout-service", "production", "+48.7s"),
            ("data-pipeline", "data", "+25.8s"),
            ("batch-reporter", "staging", "+85.4s"),
            ("log-shipper", "staging", "+97.9s")]

    spine, top = 150, 272
    for i, (wl, ns, dt) in enumerate(kids):
        by = top + i * 56
        e += box(230, by, 320, 40, f"Evicted   {wl} · {ns}", stroke=BLUE,
                 bg=F_BLUE, size=13)
        e.append(text(566, by + 11, dt, size=12, colour=GREY, font=CODE))
        e.append(arrow(spine, by + 20, 224, by + 20, colour=BLUE))
    e.append(line([(355, 214), (355, 240), (spine, 240),
                   (spine, top + 5 * 56 + 20)], colour=BLUE, head=None))

    fy = top + 6 * 56 + 14
    e.append(text(230, fy, "every child body reads:", size=13, colour=GREY))
    e.append(text(230, fy + 22, "The node had condition: [DiskPressure].",
                  size=13, colour=BLUE, font=CODE))
    e.append(text(230, fy + 48, "the edge is quoted from the capture, so you "
                                "can check it", size=13, colour=GREY))

    oy = fy + 92
    e += box(230, oy, 320, 44, "Evicted  data-pipeline · data\n"
                               "10:02:32   on node-2",
             stroke=GREY, bg=F_GREY, size=13, style="dashed")
    e.append(text(566, oy - 4, "body says \"low on resource: memory\"\n"
                               "different node, different stated cause\n"
                               "so it roots on its own and gets suppressed",
                  size=12, colour=GREY))
    e.append(text(230, oy + 56, "the control that proves the rule "
                                "discriminates", size=13, colour=GREY))

    # ---- right: 03, depth --------------------------------------------------
    bx = 800
    e.append(text(bx, 122, "03-image-pull   one cause, three deep", size=18,
                  weight="bold"))

    chain = [
        ("ScalingReplicaSet   payment-service\n"
         "\"Scaled up replica set payment-service-9e3f1a2b8 to 3\"",
         AMBER, F_AMBER, "the rollout itself. one record, zero damage."),
        ("Failed   payment-service   ×24\n"
         "\"…payment-service:v2.14.0-rc3: manifest unknown\"",
         RED, F_RED, "the mechanism. names the tag that doesn't exist."),
        ("BackOff   payment-service   ×18\n"
         "\"Back-off restarting failed container\"",
         BLUE, F_BLUE, "the consequence. this is what paged you."),
    ]
    proofs = [("the 3 failing pods all belong to replica set\n"
               "payment-service-9e3f1a2b8, created 4.4s earlier"),
              ("same pod: identity plus adjacency. the weakest\n"
               "of the three rules, and the confidence says so")]

    bw, cy = 500, 156
    for i, (label, stroke, bg, note) in enumerate(chain):
        e += box(bx, cy, bw, 62, label, stroke=stroke, bg=bg, size=13)
        e.append(text(bx, cy + 68, note, size=13, colour=stroke))
        if i < 2:
            e.append(arrow(bx + 60, cy + 96, bx + 60, cy + 150, colour=stroke))
            e.append(text(bx + 84, cy + 100, proofs[i], size=12, colour=GREY))
        cy += 186

    e.append(text(bx, cy + 4,
                  "Rule priority is dimension specificity: pod > node > "
                  "workload.\nThat ordering is what keeps this three deep. "
                  "Ordering by parent kind\nflattens the last two into "
                  "siblings, and the claim that the retry\nis caused by the "
                  "pull failure goes with it.", size=13, colour=GREY))

    scene(e, "forest-shapes", "shapes the causal forest makes")


# ------------------------------------------------------------- forest array --

def forest_array():
    parents = [-1, 0, 0, 0, -1, 4, 5]
    labels = ["node cond", "Evicted", "Evicted", "Evicted", "Scaling",
              "Failed", "BackOff"]

    e = [text(40, 20, "What the forest actually is", size=26, weight="bold"),
         text(40, 60, "One int per finding, parallel to the findings sorted "
                      "ascending by first occurrence.", size=14, colour=GREY)]

    cw, x0, cy = 168, 40, 150
    for i, p in enumerate(parents):
        col = GREY if p == -1 else INK
        e.append(text(x0 + i * cw + 4, 100, f"[{i}]  {labels[i]}", size=12,
                      colour=GREY, font=CODE))
        e += box(x0 + i * cw, cy, cw - 14, 54, f"{p}", stroke=col,
                 bg=F_GREY if p == -1 else "transparent", size=20, font=CODE)
    e.append(text(x0 + 7 * cw + 10, cy + 16, "-1 is a root", size=13,
                  colour=GREY))

    # Arcs run below the cells. Lanes are assigned greedily by x-overlap, so no
    # two arcs ever share a horizontal run. Children landing on the same parent
    # are staggered sideways, and the final leg is long enough for the
    # arrowhead to take its angle from a vertical segment.
    edges = sorted(((i, p) for i, p in enumerate(parents) if p != -1),
                   key=lambda ip: ip[0] - ip[1])
    lanes, landed = [], {}
    placed = []
    for i, p in edges:
        lo, hi = min(i, p), max(i, p)
        for n, taken in enumerate(lanes):
            if all(hi < a or lo > b for a, b in taken):
                taken.append((lo, hi))
                lane_no = n
                break
        else:
            lanes.append([(lo, hi)])
            lane_no = len(lanes) - 1
        nth = landed.get(p, 0)
        landed[p] = nth + 1
        placed.append((i, p, lane_no, nth))

    top, step = cy + 96, 34
    for i, p, lane_no, nth in placed:
        sx = x0 + i * cw + (cw - 14) / 2
        dx = x0 + p * cw + (cw - 14) / 2 + (nth - 1) * 16
        lane = top + lane_no * step
        e.append(line([(sx, cy + 56), (sx, lane), (dx, lane), (dx, cy + 62)],
                      colour=VIOLET))

    iy = top + len(lanes) * step + 44
    e.append(text(x0, iy, "Edges[i].Parent < i", size=20, colour=VIOLET,
                  font=CODE, weight="bold"))
    e.append(text(x0 + 260, iy + 4, "always, for every i", size=15,
                  colour=GREY))
    e.append(text(x0, iy + 34,
                  "Build only ever scans backwards from i, so the invariant "
                  "holds by construction. Almost everything\nI would otherwise "
                  "have to defend with code falls out of that one line:",
                  size=14))

    rows = [
        ("cycles are structurally impossible",
         "no visited set, no cycle check, no error path to test"),
        ("Findings is already topological",
         "sorting by time IS the topological sort"),
        ("Children(i) scans forward from i+1",
         "no adjacency lists, no allocation"),
        ("RootOf is an integer loop",
         "2.3 ns and zero allocations at n=1000"),
    ]
    ry = iy + 96
    for i, (left, right) in enumerate(rows):
        e.append(text(x0 + 16, ry + i * 30, left, size=14, colour=VIOLET,
                      font=CODE))
        e.append(text(x0 + 500, ry + i * 30, right, size=13, colour=GREY))

    e.append(rule(x0, x0 + 1130, ry + 4 * 30 + 16))
    e.append(text(x0, ry + 4 * 30 + 34,
                  "n counts findings, so it tracks distinct failure modes. A "
                  "cluster emitting 10× the events has roughly the same n.\n"
                  "Observed across these six captures: 20,000 records in, 3 to "
                  "10 findings out.", size=13, colour=GREY))

    scene(e, "forest-array", "the parent array")


# ------------------------------------------------------------------ thread --

def thread():
    e = [text(40, 20, "Pulling the thread", size=26, weight="bold"),
         text(40, 60, "3am. You were paged for one service. You want the trail "
                      "from your symptom back to its origin, and nothing else.",
              size=14, colour=GREY),
         text(40, 122, "$ daiquiri --trace auth-service 05-test-b.jsonl",
              size=16, colour=GREEN, font=CODE)]

    e.append(text(120, 190, "the edge, quoted from the capture:", size=13,
                  colour=GREY))
    e.append(text(120, 214,
                  "node says   \"Node node-4 status is now: NodeHasDiskPressure\"\n"
                  "pod says    \"The node had condition: [DiskPressure].\"",
                  size=12, font=CODE))

    y2 = 284
    e += box(120, y2, 430, 62, "node-4   NodeHasDiskPressure\n10:15:00.000",
             stroke=RED, bg=F_RED, size=14)
    e.append(text(568, y2 + 14, "ROOT CAUSE\nEdges[i].Parent == -1", size=13,
                  colour=RED, font=CODE))

    e.append(arrow(335, y2 + 152, 335, y2 + 76, colour=VIOLET, width=3))
    e.append(text(354, y2 + 90, "i = Edges[i].Parent", size=15, colour=VIOLET,
                  font=CODE))
    e.append(text(354, y2 + 116, "one integer hop", size=13, colour=GREY))

    y = y2 + 164
    e += box(120, y, 430, 62,
             "Evicted   auth-service · production\n10:16:25.429   ×1 · 1 pod",
             stroke=BLUE, bg=F_BLUE, size=14)
    e.append(text(568, y + 22, "YOU ARE HERE", size=14, colour=BLUE, font=CODE))
    e.append(text(120, y + 76, "auth-service did nothing wrong. No code change "
                               "will fix this.", size=13, colour=GREY))

    e += box(790, 180, 480, 132,
             "--trace narrows, and says what it left out\n\n"
             "TRACING auth-service - showing 1 of 1 incident\n\n"
             "incidents that don't involve you are counted in the header.\n"
             "a filtered report that stays quiet about the filter\n"
             "can mislead by omission.", stroke=GREEN, bg=F_GREEN, size=13)

    vy = 372
    e += box(790, vy, 480, 44, "Time is a veto, never a proposal",
             stroke=VIOLET, bg=F_VIOLET, size=16)
    e.append(text(790, vy + 62,
                  "No rule may say \"these happened near each other, so they're\n"
                  "related.\" Every rule needs a shared dimension first: same "
                  "pod,\nsame node, same ReplicaSet. Time is applied afterwards "
                  "and can\nonly reject.", size=13))
    e.append(text(790, vy + 158, "04-test-a is why:", size=13, colour=GREY))
    e += box(790, vy + 184, 230, 44, "data-pipeline\n3× Unhealthy", stroke=GREY,
             bg=F_GREY, size=12)
    e += box(1040, vy + 184, 230, 44, "checkout-service\n222× Unhealthy",
             stroke=BLUE, bg=F_BLUE, size=12)
    e.append(text(790, vy + 240,
                  "same reason, same minutes, no relationship at all.\n"
                  "proximity alone reports one incident where there are two.",
                  size=13, colour=GREY))

    wy = vy + 316
    e.append(text(790, wy, "The window: 300s, and where that number came from",
                  size=16, weight="bold"))
    e.append(line([(800, wy + 64), (1270, wy + 64)], colour=INK, head=None))
    for px, lab, col in [(808, "0s", GREY), (904, "+97.9s", GREEN),
                         (1046, "300s", VIOLET), (1200, "+600s", RED)]:
        e.append(line([(px, wy + 52), (px, wy + 76)], colour=col, head=None,
                      width=3))
        e.append(text(px - 24, wy + 82, lab, size=12, colour=col, font=CODE))
    e.append(text(818, wy + 28, "every true edge lands in here", size=12,
                  colour=GREEN))
    e.append(text(1120, wy + 28, "nearest false one", size=12, colour=RED))
    e.append(text(790, wy + 112,
                  "300s: 3× the largest true edge, half the smallest false one. "
                  "One constant for all three rules.\nPer-rule windows would be "
                  "tuned to this corpus and therefore overfitted to it.",
                  size=12, colour=VIOLET))

    scene(e, "thread", "pulling the thread")


if __name__ == "__main__":
    pipeline()
    forest_shapes()
    forest_array()
    thread()
