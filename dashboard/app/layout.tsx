import type { Metadata } from "next";
import { Fraunces, Geist, Geist_Mono, Syne } from "next/font/google";
import { Sidebar } from "@/components/sidebar";
import "./globals.css";

const sans = Geist({ subsets: ["latin"], variable: "--font-sans" });
const mono = Geist_Mono({ subsets: ["latin"], variable: "--font-mono" });
const display = Syne({ subsets: ["latin"], variable: "--font-display" });
const serif = Fraunces({ subsets: ["latin"], variable: "--font-serif" });

export const metadata: Metadata = {
  title: "Durable Flow",
  description: "Watch workflows recover after a worker crash.",
};

const themeBoot = `(function(){try{var t=localStorage.getItem("df-theme");if(t!=="light"&&t!=="dark"){t=matchMedia("(prefers-color-scheme: dark)").matches?"dark":"light"}document.documentElement.setAttribute("data-theme",t);var s=localStorage.getItem("df-sidebar");document.documentElement.setAttribute("data-sidebar",s==="collapsed"?"collapsed":"expanded")}catch(e){document.documentElement.setAttribute("data-theme","light");document.documentElement.setAttribute("data-sidebar","expanded")}})();`;

export default function RootLayout({ children }: LayoutProps<"/">) {
  return (
    <html lang="en" className={`${sans.variable} ${mono.variable} ${display.variable} ${serif.variable}`} suppressHydrationWarning>
      <head>
        <script dangerouslySetInnerHTML={{ __html: themeBoot }} />
      </head>
      <body>
        <div className="app">
          <Sidebar />
          <div className="content">{children}</div>
        </div>
      </body>
    </html>
  );
}
