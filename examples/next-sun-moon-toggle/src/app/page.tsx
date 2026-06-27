"use client";

import { useEffect, useState } from "react";
import ThemeToggle from "@/components/ThemeToggle";

export default function Home() {
  const [isDark, setIsDark] = useState<boolean | null>(null);
  const resolvedIsDark = isDark ?? false;
  const modeLabel =
    isDark === null ? "Theme mode" : isDark ? "Dark mode" : "Light mode";

  useEffect(() => {
    const frame = window.requestAnimationFrame(() => {
      setIsDark(document.documentElement.classList.contains("dark"));
    });

    return () => window.cancelAnimationFrame(frame);
  }, []);

  useEffect(() => {
    if (isDark === null) {
      return;
    }

    const root = document.documentElement;
    root.classList.toggle("dark", isDark);
    root.style.colorScheme = isDark ? "dark" : "light";
    localStorage.setItem("theme", isDark ? "dark" : "light");
  }, [isDark]);

  return (
    <main className="theme-shell">
      <section className="toggle-stage" aria-labelledby="demo-title">
        <div className="mode-copy">
          <p className="mode-kicker">Next.js theme control</p>
          <h1 id="demo-title">Sun and Moon Toggle</h1>
          <p className="mode-description">
            {isDark === null
              ? "A theme control that respects saved preference before React hydrates."
              : isDark
                ? "A calm night surface with a crescent control state."
                : "A bright day surface with a solar control state."}
          </p>
        </div>

        <ThemeToggle
          isDark={resolvedIsDark}
          onToggle={() => setIsDark((current) => !(current ?? false))}
        />

        <p className="mode-status" aria-live="polite">
          {modeLabel}
        </p>
      </section>
    </main>
  );
}
