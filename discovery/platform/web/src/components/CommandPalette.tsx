import { useEffect, useId, useMemo, useRef, useState, type ReactNode } from "react";
import { api } from "../api/client";
import type { Resource } from "../api/types";
import { href } from "../lib/hooks";
import { useApp } from "../lib/context";
import { Icon, TypeIcon } from "./Icon";

export interface PaletteCommand {
  id: string;
  label: string;
  hint?: string;
  icon: string;
  run: () => void;
}

interface Item {
  key: string;
  label: string;
  hint?: string;
  icon: ReactNode;
  run: () => void;
}

/**
 * CommandPalette (Ctrl/Cmd+K or "/") jumps to pages, domains and resources by
 * name. It is an ARIA combobox: arrow keys move, Enter opens, Escape closes.
 */
export function CommandPalette(props: { open: boolean; onClose: () => void; commands: PaletteCommand[] }) {
  // Mounting the body only while open gives every opening fresh state.
  return props.open ? <PaletteBody onClose={props.onClose} commands={props.commands} /> : null;
}

function PaletteBody({ onClose, commands }: { onClose: () => void; commands: PaletteCommand[] }) {
  const [q, setQ] = useState("");
  const [active, setActive] = useState(0);
  const [result, setResult] = useState<{ q: string; items: Resource[] }>();
  const input = useRef<HTMLInputElement>(null);
  const listId = useId();
  const { catalog } = useApp();
  const needleQ = q.trim();
  const found = useMemo(() => (result && result.q === needleQ ? result.items : []), [result, needleQ]);

  useEffect(() => {
    input.current?.focus();
  }, []);

  // Any navigation (link, back button, typed URL) dismisses the palette.
  useEffect(() => {
    window.addEventListener("hashchange", onClose);
    return () => window.removeEventListener("hashchange", onClose);
  }, [onClose]);

  useEffect(() => {
    if (needleQ.length < 2) return;
    let live = true;
    const t = setTimeout(() => {
      api
        .resources({ q: needleQ, status: "active", limit: 8 })
        .then((p) => live && setResult({ q: needleQ, items: p.items }))
        .catch(() => live && setResult({ q: needleQ, items: [] }));
    }, 150);
    return () => {
      live = false;
      clearTimeout(t);
    };
  }, [needleQ]);

  const items = useMemo<Item[]>(() => {
    const needle = q.trim().toLowerCase();
    const match = (s: string) => !needle || s.toLowerCase().includes(needle);
    const go = (h: string) => () => {
      window.location.hash = h;
      onClose();
    };
    const cmds: Item[] = commands
      .filter((c) => match(c.label))
      .map((c) => ({ key: c.id, label: c.label, hint: c.hint, icon: <Icon name={c.icon} />, run: () => (c.run(), onClose()) }));
    const domains: Item[] = (catalog?.domains ?? [])
      .filter((d) => d.name !== "other" && match(d.displayName))
      .map((d) => ({ key: `d-${d.name}`, label: `Explore ${d.displayName}`, hint: d.description, icon: <Icon name={d.name} />, run: go(href("resources", undefined, { domain: d.name })) }));
    const res: Item[] = found.map((r) => ({
      key: r.id,
      label: r.name,
      hint: `${r.type} · ${r.region}`,
      icon: <TypeIcon type={r.type} />,
      run: go(href("resources", r.id)),
    }));
    return [...cmds, ...domains, ...res].slice(0, 20);
  }, [q, commands, catalog, found, onClose]);

  const clamp = (i: number) => (items.length === 0 ? 0 : (i + items.length) % items.length);

  return (
    <div className="palette-backdrop" onMouseDown={onClose}>
      <div className="palette" role="dialog" aria-modal="true" aria-label="Command palette" onMouseDown={(e) => e.stopPropagation()}>
        <div className="palette-input">
          <Icon name="search" />
          <input
            ref={input}
            role="combobox"
            aria-expanded="true"
            aria-controls={listId}
            aria-activedescendant={items[active] ? `${listId}-${active}` : undefined}
            aria-label="Search pages and resources"
            placeholder="Search pages, domains and resources…"
            value={q}
            onChange={(e) => {
              setQ(e.target.value);
              setActive(0);
            }}
            onKeyDown={(e) => {
              if (e.key === "ArrowDown") {
                e.preventDefault();
                setActive((a) => clamp(a + 1));
              } else if (e.key === "ArrowUp") {
                e.preventDefault();
                setActive((a) => clamp(a - 1));
              } else if (e.key === "Enter") {
                e.preventDefault();
                items[active]?.run();
              } else if (e.key === "Escape") {
                onClose();
              }
            }}
          />
          <kbd>Esc</kbd>
        </div>
        <ul id={listId} role="listbox" className="palette-list">
          {items.length === 0 && (
            <li className="palette-empty" role="status">
              {needleQ.length < 2 ? "Type to search" : result?.q !== needleQ ? "Searching…" : "No matches"}
            </li>
          )}
          {items.map((it, i) => (
            <li
              key={it.key}
              id={`${listId}-${i}`}
              role="option"
              aria-selected={i === active}
              className={i === active ? "active" : ""}
              onMouseEnter={() => setActive(i)}
              onClick={it.run}
            >
              {it.icon}
              <span className="palette-label">{it.label}</span>
              {it.hint && <span className="palette-hint">{it.hint}</span>}
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}
