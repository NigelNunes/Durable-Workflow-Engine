"use client";

import { useSyncExternalStore } from "react";

type Theme = "light" | "dark";

function subscribe(onStoreChange: () => void) {
  const observer = new MutationObserver(onStoreChange);
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
  return () => observer.disconnect();
}

function getTheme(): Theme {
  return document.documentElement.getAttribute("data-theme") === "dark" ? "dark" : "light";
}

function applyTheme(theme: Theme) {
  document.documentElement.setAttribute("data-theme", theme);
  window.localStorage.setItem("df-theme", theme);
}

export function ThemeToggle({ compact = false }: { compact?: boolean }) {
  const theme = useSyncExternalStore(subscribe, getTheme, () => "light" as Theme);

  function toggle() {
    applyTheme(theme === "dark" ? "light" : "dark");
  }

  return (
    <button
      className={compact ? "theme-toggle compact" : "theme-toggle"}
      type="button"
      onClick={toggle}
      aria-label={theme === "dark" ? "Switch to light mode" : "Switch to dark mode"}
      title={theme === "dark" ? "Dark" : "Light"}
    >
      <span className="theme-toggle-track" aria-hidden="true">
        <span className={theme === "dark" ? "theme-toggle-thumb dark" : "theme-toggle-thumb"} />
      </span>
      <span className="theme-toggle-label">{theme === "dark" ? "Dark" : "Light"}</span>
    </button>
  );
}
