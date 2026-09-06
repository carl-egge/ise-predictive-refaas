"""Shared drawing helpers for the figures in this directory.

Small on purpose. These figures are analysis output, not a plotting library:
what belongs here is only what two of them already need, so that a change to how
an axis is drawn cannot silently apply to one figure and not the other.

The SVG side exists because there is no matplotlib and no LaTeX on this machine.
It is a preview - the TikZ output is what goes in the write-up - but it is drawn
from the same numbers, so a figure that looks wrong here is wrong there too.
"""


def rects(edges, heights, frac):
    """Bars as (x0, x1, height) in data units, centred in their bin.

    One geometry function for both renderers: the SVG preview and the TikZ
    figure must not be able to disagree about what was plotted.
    """
    out = []
    for i, h in enumerate(heights):
        if h <= 0:
            continue
        mid = (edges[i] + edges[i + 1]) / 2
        half = (edges[i + 1] - edges[i]) * frac / 2
        out.append((mid - half, mid + half, h))
    return out


def steps(edges, heights):
    """The paper's bars as one step outline, so they read as a reference rather
    than as a third series of ours."""
    pts = [(edges[0], 0.0)]
    for i, h in enumerate(heights):
        pts.append((edges[i], h))
        pts.append((edges[i + 1], h))
    pts.append((edges[-1], 0.0))
    return pts


def fmt(v):
    return "%.6g" % v


def esc(s):
    return s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


class Panel:
    """One set of axes, in SVG user units, with a linear data->pixel map."""

    def __init__(self, x, y, w, h, xlim, ylim):
        self.x, self.y, self.w, self.h = x, y, w, h
        self.xlim, self.ylim = xlim, ylim
        self.out = []

    def px(self, v):
        lo, hi = self.xlim
        return self.x + (v - lo) / (hi - lo) * self.w

    def py(self, v):
        lo, hi = self.ylim
        return self.y + self.h - (v - lo) / (hi - lo) * self.h

    def frame(self, title, xlabel, ylabel, xticks, yticks, xfmt=str):
        o = self.out
        for t in yticks:  # grid first, so bars sit on top of it
            yy = self.py(t)
            o.append('<line x1="%.2f" y1="%.2f" x2="%.2f" y2="%.2f" '
                     'stroke="#e8e8e8" stroke-width="1"/>'
                     % (self.x, yy, self.x + self.w, yy))
            o.append('<text x="%.2f" y="%.2f" font-size="11" fill="#333" '
                     'text-anchor="end" dominant-baseline="middle">%d</text>'
                     % (self.x - 7, yy, t))
        for t in xticks:
            xx = self.px(t)
            o.append('<line x1="%.2f" y1="%.2f" x2="%.2f" y2="%.2f" '
                     'stroke="#333" stroke-width="1"/>'
                     % (xx, self.y + self.h, xx, self.y + self.h + 4))
            o.append('<text x="%.2f" y="%.2f" font-size="10.5" fill="#333" '
                     'text-anchor="middle">%s</text>'
                     % (xx, self.y + self.h + 17, esc(xfmt(t))))
        o.append('<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" fill="none" '
                 'stroke="#333" stroke-width="1"/>' % (self.x, self.y, self.w, self.h))
        o.append('<text x="%.2f" y="%.2f" font-size="12" fill="#111" '
                 'text-anchor="middle">%s</text>'
                 % (self.x + self.w / 2, self.y + self.h + 36, esc(xlabel)))
        o.append('<text x="%.2f" y="%.2f" font-size="12" fill="#111" '
                 'text-anchor="middle" transform="rotate(-90 %.2f %.2f)">%s</text>'
                 % (self.x - 36, self.y + self.h / 2, self.x - 36,
                    self.y + self.h / 2, esc(ylabel)))
        o.append('<text x="%.2f" y="%.2f" font-size="13" fill="#111" '
                 'text-anchor="middle" font-weight="600">%s</text>'
                 % (self.x + self.w / 2, self.y - 11, esc(title)))

    def bars(self, bars, color, opacity):
        for x0, x1, h in bars:
            px0, px1 = self.px(x0), self.px(x1)
            y0, y1 = self.py(0), self.py(h)
            self.out.append(
                '<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" fill="%s" '
                'fill-opacity="%.2f" stroke="%s" stroke-width="1"/>'
                % (px0, y1, max(px1 - px0, 0.8), y0 - y1, color, opacity, color))

    def outline(self, pts, color):
        path = "M " + " L ".join("%.2f %.2f" % (self.px(x), self.py(y)) for x, y in pts)
        self.out.append('<path d="%s" fill="none" stroke="%s" stroke-width="1.6" '
                        'stroke-dasharray="5 3"/>' % (path, color))

    def vline(self, v, color, dash="4 3"):
        self.out.append('<line x1="%.2f" y1="%.2f" x2="%.2f" y2="%.2f" stroke="%s" '
                        'stroke-width="1.4" stroke-dasharray="%s"/>'
                        % (self.px(v), self.y, self.px(v), self.y + self.h, color, dash))

    def note(self, lines, fx, fy, anchor="end"):
        x = self.x + self.w * fx
        for i, (txt, color) in enumerate(lines):
            self.out.append('<text x="%.2f" y="%.2f" font-size="11" fill="%s" '
                            'text-anchor="%s">%s</text>'
                            % (x, self.y + self.h * fy + i * 14, color, anchor, esc(txt)))

    def legend(self, entries, fx, fy):
        x, y = self.x + self.w * fx, self.y + self.h * fy
        wide = 8 + 15 + 6 + max(len(t) for t, _, _ in entries) * 6.2 + 8
        self.out.append('<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" fill="#fff" '
                        'fill-opacity="0.92" stroke="#ccc" stroke-width="1"/>'
                        % (x, y, wide, 8 + len(entries) * 17))
        for i, (label, color, dashed) in enumerate(entries):
            yy = y + 8 + i * 17
            if dashed:
                self.out.append('<line x1="%.2f" y1="%.2f" x2="%.2f" y2="%.2f" '
                                'stroke="%s" stroke-width="1.6" stroke-dasharray="5 3"/>'
                                % (x + 8, yy + 5, x + 23, yy + 5, color))
            else:
                self.out.append('<rect x="%.2f" y="%.2f" width="15" height="10" '
                                'fill="%s" fill-opacity="0.55" stroke="%s" '
                                'stroke-width="1"/>' % (x + 8, yy, color, color))
            self.out.append('<text x="%.2f" y="%.2f" font-size="11" fill="#111" '
                            'dominant-baseline="middle">%s</text>'
                            % (x + 29, yy + 5, esc(label)))


def wrap(text, width):
    lines, cur = [], ""
    for word in text.split():
        if cur and len(cur) + 1 + len(word) > width:
            lines.append(cur)
            cur = word
        else:
            cur = word if not cur else cur + " " + word
    if cur:
        lines.append(cur)
    return lines
