"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useSyncExternalStore } from "react";
import { ThemeToggle } from "@/components/theme-toggle";

const links = [
  {
    href: "/",
    label: "Home",
    match: (path: string) => path === "/",
    icon: (
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <path
          d="M4 10.5 12 4l8 6.5V20a1 1 0 0 1-1 1h-5v-6H10v6H5a1 1 0 0 1-1-1v-9.5Z"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.8"
          strokeLinejoin="round"
        />
      </svg>
    ),
  },
  {
    href: "/demo",
    label: "Live demo",
    match: (path: string) => path.startsWith("/demo") || path.startsWith("/workflows"),
    icon: (
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <path
          d="M8 5.5v13l11-6.5L8 5.5Z"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.8"
          strokeLinejoin="round"
        />
      </svg>
    ),
  },
  {
    href: "/how-it-works",
    label: "How it works",
    match: (path: string) => path.startsWith("/how-it-works"),
    icon: (
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <circle cx="12" cy="12" r="8.2" fill="none" stroke="currentColor" strokeWidth="1.8" />
        <path
          d="M12 8v4.2l2.8 1.6"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.8"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </svg>
    ),
  },
] as const;

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

export function Sidebar() {
  const path = usePathname();
  const collapsed = useSyncExternalStore(subscribeSidebar, getCollapsed, () => false);

  function toggle() {
    applyCollapsed(!collapsed);
  }

  return (
    <aside className={collapsed ? "sidebar collapsed" : "sidebar"}>
      <div className="sidebar-top">
        <div className="brand">
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
          <Link className="word" href="/" title="Durable Flow">
            <span>Durable Flow</span>
            <span className="brand-sub">Durable execution</span>
          </Link>
        </div>
      </div>

      <nav aria-label="Pages">
        {links.map((link) => {
          const current = link.match(path);
          return (
            <Link
              key={link.href}
              href={link.href}
              aria-current={current ? "page" : undefined}
              title={link.label}
            >
              <span className="nav-icon">{link.icon}</span>
              <span className="nav-label">{link.label}</span>
            </Link>
          );
        })}
      </nav>

      <div className="sidebar-foot">
        <ThemeToggle compact={collapsed} />
      </div>
    </aside>
  );
}
