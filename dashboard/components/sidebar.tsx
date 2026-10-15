"use client";

import Link from "next/link";
import { usePathname, useSearchParams } from "next/navigation";
import { Suspense, useSyncExternalStore } from "react";
import { ThemeToggle } from "@/components/theme-toggle";
import { scenarios } from "@/lib/scenarios";

function subscribeSidebar(onChange: () => void) {
  const observer = new MutationObserver(onChange);
  observer.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ["data-sidebar"],
  });
  return () => observer.disconnect();
}

function getCollapsed(): boolean {
  return document.documentElement.getAttribute("data-sidebar") === "collapsed";
}

function applyCollapsed(collapsed: boolean) {
  document.documentElement.setAttribute("data-sidebar", collapsed ? "collapsed" : "expanded");
  try {
    window.localStorage.setItem("df-sidebar", collapsed ? "collapsed" : "expanded");
  } catch {
    /* ignore */
  }
}

function IconHome() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path
        d="M4 10.5 12 4l8 6.5V20a1 1 0 0 1-1 1h-5v-6H10v6H5a1 1 0 0 1-1-1v-9.5Z"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.7"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function IconPlay() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M8 5.5v13l11-6.5L8 5.5Z" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinejoin="round" />
    </svg>
  );
}

function IconClock() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <circle cx="12" cy="12" r="8.2" fill="none" stroke="currentColor" strokeWidth="1.7" />
      <path
        d="M12 8v4.2l2.8 1.6"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.7"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function SidebarNav({ collapsed }: { collapsed: boolean }) {
  const path = usePathname();
  const search = useSearchParams();
  const activeScenario = search.get("scenario");
  const home = path === "/";
  const demo = path.startsWith("/demo") || path.startsWith("/workflows");
  const how = path.startsWith("/how-it-works");
  const demoOpen = demo && !collapsed;

  return (
    <nav className="side-nav" aria-label="Pages">
      <p className="side-section">Navigate</p>

      <div className="side-tree-root">
        <Link className={home ? "side-link on" : "side-link"} href="/" aria-current={home ? "page" : undefined} title="Home">
          <span className="side-spacer" aria-hidden="true" />
          <span className="side-ico">
            <IconHome />
          </span>
          <span className="side-text">Home</span>
        </Link>

        <div className={demoOpen ? "side-branch open" : "side-branch"}>
          <Link
            className={demo ? "side-link on" : "side-link"}
            href="/demo"
            aria-current={demo && !activeScenario ? "page" : undefined}
            title="Live demo"
          >
            <span className={demoOpen ? "side-tri open" : "side-tri"} aria-hidden="true" />
            <span className="side-ico">
              <IconPlay />
            </span>
            <span className="side-text">Live demo</span>
          </Link>

          <div className={demoOpen ? "side-tree open" : "side-tree"} aria-hidden={!demoOpen}>
            <div className="side-tree-inner">
              {scenarios.map((scenario, index) => {
                const last = index === scenarios.length - 1;
                const href = `/demo?scenario=${scenario.type}`;
                const selected = demo && activeScenario === scenario.type;
                const leafClass = ["side-leaf", selected ? "on" : "", last ? "last" : ""]
                  .filter(Boolean)
                  .join(" ");
                return (
                  <Link
                    key={scenario.type}
                    href={href}
                    className={leafClass}
                    aria-current={selected ? "page" : undefined}
                    title={scenario.label}
                  >
                    <span className="side-dot" aria-hidden="true" />
                    <span className="side-leaf-text">{scenario.short}</span>
                  </Link>
                );
              })}
            </div>
          </div>
        </div>

        <Link
          className={how ? "side-link on" : "side-link"}
          href="/how-it-works"
          aria-current={how ? "page" : undefined}
          title="How it works"
        >
          <span className="side-spacer" aria-hidden="true" />
          <span className="side-ico">
            <IconClock />
          </span>
          <span className="side-text">How it works</span>
        </Link>
      </div>
    </nav>
  );
}

export function Sidebar() {
  const collapsed = useSyncExternalStore(subscribeSidebar, getCollapsed, () => false);

  function toggle() {
    applyCollapsed(!collapsed);
  }

  return (
    <aside className={collapsed ? "sidebar collapsed" : "sidebar"}>
      <div className="side-brand">
        <button
          type="button"
          className="mark-btn"
          onClick={toggle}
          aria-expanded={!collapsed}
          aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
          title={collapsed ? "Expand sidebar" : "Collapse sidebar"}
        >
          <svg className="mark" viewBox="0 0 32 32" aria-hidden="true">
            <circle cx="7" cy="16" r="3.2" fill="currentColor" />
            <circle cx="16" cy="8" r="3.2" fill="currentColor" />
            <circle cx="25" cy="20" r="3.2" fill="currentColor" />
            <path
              d="M9.5 14.5 L13.5 10 M18.5 10.5 L22.5 17"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.6"
            />
          </svg>
        </button>
        <Link className="side-brand-copy" href="/" title="Durable Flow">
          <span className="side-brand-name">Durable Flow</span>
          <span className="side-brand-tag">Durable execution</span>
        </Link>
      </div>

      <Suspense fallback={<nav className="side-nav" aria-label="Pages" />}>
        <SidebarNav collapsed={collapsed} />
      </Suspense>

      <div className="side-foot">
        <ThemeToggle compact={collapsed} />
      </div>
    </aside>
  );
}
