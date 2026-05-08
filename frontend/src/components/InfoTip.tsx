import { useEffect, useRef, useState } from "react";

interface InfoTipProps {
  text: string;
}

/**
 * Small "i" icon that reveals a short help tooltip on hover or click.
 * Click toggles a persistent popover; hover shows it transiently.
 * Click-outside dismisses the persistent state.
 */
export default function InfoTip({ text }: InfoTipProps) {
  const [pinned, setPinned] = useState(false);
  const [hover, setHover] = useState(false);
  const ref = useRef<HTMLSpanElement>(null);

  useEffect(() => {
    if (!pinned) return;
    const onDocClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setPinned(false);
      }
    };
    document.addEventListener("mousedown", onDocClick);
    return () => document.removeEventListener("mousedown", onDocClick);
  }, [pinned]);

  const open = pinned || hover;

  return (
    <span
      ref={ref}
      className="relative inline-flex"
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
    >
      <button
        type="button"
        aria-label="More info"
        onClick={(e) => {
          e.preventDefault();
          setPinned((v) => !v);
        }}
        className="inline-flex items-center justify-center w-[14px] h-[14px] rounded-full border border-ink-2 text-ink-2 hover:border-signal hover:text-signal font-mono text-[10px] leading-none cursor-help"
      >
        i
      </button>
      {open && (
        <span
          role="tooltip"
          className="absolute left-full top-1/2 -translate-y-1/2 ml-2 z-50 w-64 p-2 panel shadow-lg font-mono text-[11px] leading-snug text-ink-1 whitespace-normal"
        >
          {text}
        </span>
      )}
    </span>
  );
}
