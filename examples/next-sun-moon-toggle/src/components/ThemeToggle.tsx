"use client";

type ThemeToggleProps = {
  isDark: boolean;
  onToggle: () => void;
};

export default function ThemeToggle({ isDark, onToggle }: ThemeToggleProps) {
  return (
    <button
      type="button"
      className="theme-toggle"
      aria-label={`Switch to ${isDark ? "light" : "dark"} mode`}
      aria-pressed={isDark}
      data-mode={isDark ? "dark" : "light"}
      onClick={onToggle}
    >
      <span className="toggle-sky" aria-hidden="true">
        <span className="sun-disc">
          <span className="sun-rays" />
        </span>
        <span className="moon-disc">
          <span className="moon-crater moon-crater-large" />
          <span className="moon-crater moon-crater-small" />
        </span>
        <span className="star star-one" />
        <span className="star star-two" />
        <span className="star star-three" />
      </span>
      <span className="toggle-knob" aria-hidden="true" />
    </button>
  );
}
