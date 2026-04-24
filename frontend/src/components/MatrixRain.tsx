// PRD-43: Matrix-rain Canvas background for the login page.
//
// Draws falling katakana + alphanumeric glyphs, fading the previous
// frame with a low-alpha overlay each tick so trails gradually die
// off. Scales with its parent container via ResizeObserver.
// Respects prefers-reduced-motion (renders a single static frame).
//
// All visual knobs are props with sensible defaults so callers can
// tune speed, size, color, and background without editing the
// component.

import { useEffect, useRef } from "react";

// Glyph set: half-width katakana + digits + Latin uppercase. Matches
// the Matrix film's alphabet fairly closely.
const GLYPHS =
  "アイウエオカキクケコサシスセソタチツテトナニヌネノハヒフヘホマミムメモヤユヨラリルレロワヲン" +
  "0123456789" +
  "ABCDEFGHIJKLMNOPQRSTUVWXYZ";

export type MatrixRainProps = {
  /** Character cell size in pixels. Smaller = denser columns. Default 16. */
  fontSize?: number;
  /** Milliseconds between frames. Smaller = faster rain. Default 55. */
  frameIntervalMs?: number;
  /** Trail-fade alpha per frame in [0, 1]. Smaller = longer trails. Default 0.08. */
  trailFadeAlpha?: number;
  /**
   * Glyph color. Any valid CSS color (e.g. "#39ff88", "rgb(57 255 136)").
   * If omitted, reads `--signal` from the theme and renders as
   * `rgb(<var>)`, so the rain matches the app accent.
   */
  color?: string;
  /**
   * Background color. Any valid CSS color. Also used as the per-frame
   * fade layer (converted to rgba with the trail alpha), so it should
   * be opaque when specified. Defaults to "#000".
   */
  backgroundColor?: string;
};

// Convert a CSS color string into `r, g, b` components so we can apply
// a per-frame alpha. Works for hex (#rgb / #rrggbb), rgb()/rgba(), and
// whitespace-separated "r g b" values read from CSS custom properties.
function parseColor(input: string): { r: number; g: number; b: number } | null {
  const s = input.trim();

  // Hex: #rgb or #rrggbb
  if (s.startsWith("#")) {
    const hex = s.slice(1);
    if (hex.length === 3) {
      const r = parseInt(hex[0] + hex[0], 16);
      const g = parseInt(hex[1] + hex[1], 16);
      const b = parseInt(hex[2] + hex[2], 16);
      return { r, g, b };
    }
    if (hex.length === 6) {
      return {
        r: parseInt(hex.slice(0, 2), 16),
        g: parseInt(hex.slice(2, 4), 16),
        b: parseInt(hex.slice(4, 6), 16),
      };
    }
    return null;
  }

  // rgb()/rgba() or whitespace-separated triplet (Tailwind-style).
  const nums = s.match(/\d+(?:\.\d+)?/g);
  if (nums && nums.length >= 3) {
    return { r: +nums[0], g: +nums[1], b: +nums[2] };
  }
  return null;
}

function signalFromTheme(): string {
  if (typeof window === "undefined") return "57 255 136";
  const v = getComputedStyle(document.documentElement).getPropertyValue("--signal").trim();
  return v || "57 255 136";
}

export default function MatrixRain({
  fontSize = 16,
  frameIntervalMs = 55,
  trailFadeAlpha = 0.08,
  color,
  backgroundColor = "#000",
}: MatrixRainProps) {
  const canvasRef = useRef<HTMLCanvasElement>(null);

  // Stash props in refs so the effect's interval callback picks up
  // updates without resetting the column state.
  const fontSizeRef = useRef(fontSize);
  const trailAlphaRef = useRef(trailFadeAlpha);
  const colorRef = useRef<string>("");
  const bgRef = useRef<string>(backgroundColor);

  fontSizeRef.current = fontSize;
  trailAlphaRef.current = trailFadeAlpha;
  colorRef.current = color ?? `rgb(${signalFromTheme()})`;
  bgRef.current = backgroundColor;

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    const dpr = window.devicePixelRatio || 1;
    let columns: number[] = []; // per-column head y-offset (in cells)
    const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

    function resize() {
      if (!canvas || !ctx) return;
      const { clientWidth: w, clientHeight: h } = canvas;
      canvas.width = Math.floor(w * dpr);
      canvas.height = Math.floor(h * dpr);
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      const cell = fontSizeRef.current;
      const colCount = Math.ceil(w / cell);
      columns = new Array(colCount).fill(0).map(() => Math.floor(Math.random() * (h / cell)));
      // Paint the initial background so fades composite correctly.
      ctx.fillStyle = bgRef.current;
      ctx.fillRect(0, 0, w, h);
    }

    resize();
    const ro = new ResizeObserver(resize);
    ro.observe(canvas);

    function draw() {
      if (!canvas || !ctx) return;
      const w = canvas.clientWidth;
      const h = canvas.clientHeight;
      const cell = fontSizeRef.current;

      // Fade previous frame with the background color at low alpha.
      const bg = parseColor(bgRef.current);
      if (bg) {
        ctx.fillStyle = `rgba(${bg.r}, ${bg.g}, ${bg.b}, ${trailAlphaRef.current})`;
      } else {
        ctx.fillStyle = `rgba(0, 0, 0, ${trailAlphaRef.current})`;
      }
      ctx.fillRect(0, 0, w, h);

      ctx.font = `${cell}px "Geist Mono", ui-monospace, monospace`;
      ctx.textBaseline = "top";
      ctx.fillStyle = colorRef.current;

      for (let i = 0; i < columns.length; i++) {
        const y = columns[i] * cell;
        const glyph = GLYPHS.charAt(Math.floor(Math.random() * GLYPHS.length));
        ctx.fillText(glyph, i * cell, y);

        // Reset the column with some probability once it's past the
        // bottom so columns don't march in lockstep.
        if (y > h && Math.random() > 0.975) {
          columns[i] = 0;
        } else {
          columns[i]++;
        }
      }
    }

    if (reducedMotion) {
      draw();
      return () => ro.disconnect();
    }

    const interval = window.setInterval(draw, frameIntervalMs);
    return () => {
      window.clearInterval(interval);
      ro.disconnect();
    };
    // frameIntervalMs is captured in the closure; changing it requires
    // restarting the interval. fontSize/trailFadeAlpha/color/backgroundColor
    // flow through refs so they update live without restart.
  }, [frameIntervalMs]);

  return (
    <canvas
      ref={canvasRef}
      aria-hidden="true"
      className="absolute inset-0 w-full h-full pointer-events-none"
      style={{ backgroundColor }}
    />
  );
}
