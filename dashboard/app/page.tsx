"use client";

import { useSyncExternalStore } from "react";
import GlassHeadlineHero from "@/components/ui/glass-headline-hero";

/** Current bright field — used in light mode. */
const LIGHT_COLORS = ["#07090d", "#1a4f92", "#8eb6e8", "#3cba74", "#f0d48a"];

/** Reference field: deep purple ground, orange / pink / blue / cream accents. */
const DARK_COLORS = ["#0D0A14", "#FF5A1F", "#FF9EC1", "#2F4CFF", "#FFE6B8"];

function subscribeTheme(onChange: () => void) {
  const observer = new MutationObserver(onChange);
  observer.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ["data-theme"],
  });
  return () => observer.disconnect();
}

function getTheme(): "light" | "dark" {
  return document.documentElement.getAttribute("data-theme") === "dark"
    ? "dark"
    : "light";
}

export default function HomePage() {
  const theme = useSyncExternalStore(subscribeTheme, getTheme, () => "light");

  return (
    <main className="landing">
      <GlassHeadlineHero
        eyebrow="Durable execution"
        title="Durable Flow"
        description="A Go workflow engine with one Postgres log. Crash a worker mid-run — another finishes the order. The work survives."
        primaryAction={{ label: "Open live demo", href: "/demo" }}
        secondaryAction={{ label: "How it works", href: "/how-it-works" }}
        colors={theme === "dark" ? DARK_COLORS : LIGHT_COLORS}
        height="100svh"
      />
    </main>
  );
}
