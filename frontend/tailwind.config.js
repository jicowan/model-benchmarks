/** @type {import('tailwindcss').Config} */
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  darkMode: "class",
  theme: {
    extend: {
      fontFamily: {
        sans: [
          "Geist",
          "ui-sans-serif",
          "system-ui",
          "-apple-system",
          "BlinkMacSystemFont",
          "sans-serif",
        ],
        mono: [
          "Geist Mono",
          "ui-monospace",
          "SFMono-Regular",
          "Menlo",
          "Monaco",
          "monospace",
        ],
      },
      fontFeatureSettings: {
        tabular: '"tnum", "cv11"',
      },
      colors: {
        // Semantic tokens driven by CSS variables (defined in index.css)
        surface: {
          0: "rgb(var(--surface-0) / <alpha-value>)",
          1: "rgb(var(--surface-1) / <alpha-value>)",
          2: "rgb(var(--surface-2) / <alpha-value>)",
        },
        line: {
          DEFAULT: "rgb(var(--line) / <alpha-value>)",
          strong: "rgb(var(--line-strong) / <alpha-value>)",
        },
        ink: {
          0: "rgb(var(--ink-0) / <alpha-value>)",
          1: "rgb(var(--ink-1) / <alpha-value>)",
          2: "rgb(var(--ink-2) / <alpha-value>)",
        },
        signal: "rgb(var(--signal) / <alpha-value>)",
        warn: "rgb(var(--warn) / <alpha-value>)",
        danger: "rgb(var(--danger) / <alpha-value>)",
        info: "rgb(var(--info) / <alpha-value>)",
      },
      borderRadius: {
        none: "0",
        sm: "2px",
        DEFAULT: "3px",
      },
      boxShadow: {
        card: "0 0 0 1px rgb(var(--line) / 1)",
        "card-strong": "0 0 0 1px rgb(var(--line-strong) / 1)",
      },
      letterSpacing: {
        mech: "0.01em",
        widemech: "0.08em",
      },
      keyframes: {
        pulse_signal: {
          "0%, 100%": { opacity: "1" },
          "50%": { opacity: "0.4" },
        },
        enter: {
          "0%": { opacity: "0", transform: "translateY(4px)" },
          "100%": { opacity: "1", transform: "translateY(0)" },
        },
        "hal-iris": {
          "0%, 100%": { transform: "scale(1)", opacity: "1", boxShadow: "0 0 4px 1px rgb(var(--signal) / 0.4)" },
          "50%": { transform: "scale(0.7)", opacity: "0.6", boxShadow: "0 0 8px 3px rgb(var(--signal) / 0.6)" },
        },
      },
      animation: {
        pulse_signal: "pulse_signal 1.6s ease-in-out infinite",
        enter: "enter 320ms cubic-bezier(0.2, 0.8, 0.2, 1) both",
        "hal-iris": "hal-iris 2.4s ease-in-out infinite",
      },
    },
  },
  plugins: [],
};
