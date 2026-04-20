import { useEffect, useState } from "react";
import { getTheme, setTheme } from "../theme";
import type { Theme } from "../theme";

export default function ThemeToggle() {
  const [theme, setT] = useState<Theme>(() => getTheme());

  useEffect(() => {
    setTheme(theme);
  }, [theme]);

  const toggle = () => setT(theme === "dark" ? "light" : "dark");

  return (
    <button
      onClick={toggle}
      title={`Switch to ${theme === "dark" ? "light" : "dark"} mode`}
      className="w-9 h-9 flex items-center justify-center border border-line bg-surface-1 text-ink-1 hover:text-ink-0 hover:bg-surface-2 hover:border-line-strong transition-colors"
      aria-label="Toggle theme"
    >
      {theme === "dark" ? (
        /* moon */
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="square">
          <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" />
        </svg>
      ) : (
        /* sun */
        <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="square">
          <circle cx="12" cy="12" r="4" />
          <path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41" />
        </svg>
      )}
    </button>
  );
}
