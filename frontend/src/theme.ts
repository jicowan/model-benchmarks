// Theme management — dark/light with localStorage persistence.

export type Theme = "dark" | "light";

const STORAGE_KEY = "accelbench.theme";

export function getTheme(): Theme {
  if (typeof window === "undefined") return "dark";
  const saved = localStorage.getItem(STORAGE_KEY) as Theme | null;
  if (saved === "dark" || saved === "light") return saved;
  // Default to dark — industrial preference
  return "dark";
}

export function setTheme(t: Theme) {
  localStorage.setItem(STORAGE_KEY, t);
  const root = document.documentElement;
  if (t === "dark") root.classList.add("dark");
  else root.classList.remove("dark");
}

export function initTheme() {
  setTheme(getTheme());
}
