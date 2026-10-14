import type { Metadata } from "next";
import Link from "next/link";
import "./globals.css";

export const metadata: Metadata = {
  title: "Durable Flow",
  description: "Watch workflows recover after a worker crash.",
};

export default function RootLayout({ children }: LayoutProps<"/">) {
  return (
    <html lang="en">
      <body>
        <div className="shell">
          <header className="top">
            <h1>Durable Flow</h1>
            <nav className="nav">
              <Link href="/">Live demo</Link>
              <Link href="/how-it-works">How it works</Link>
            </nav>
          </header>
          {children}
        </div>
      </body>
    </html>
  );
}
