import { useEffect, useState } from "react";

/**
 * Motion for the guest home page: scroll reveal, count-up numerals, a spotlight
 * on cards, and the scroll-driven state of its fixed parts (progress bar,
 * section rail, call-to-action dock, pinned "how it works" panel).
 *
 * It works on the rendered page: it only toggles classes and custom properties
 * on elements whose class names React never changes, and undoes all of it on
 * unmount. With `prefers-reduced-motion` the reveals, count-ups and parallax
 * are skipped and the page is simply there; the fixed parts still work.
 */

/** The sections the rail links to, in page order. */
export const landingSections = [
  { id: "top", label: "Top" },
  { id: "plans", label: "Pricing" },
  { id: "cheap", label: "Calculator" },
  { id: "what", label: "What we do" },
  { id: "how", label: "How it works" },
  { id: "included", label: "Included" },
  { id: "usecases", label: "GTX 1060" },
  { id: "offer", label: "Catalog" },
  { id: "who", label: "Who it is for" },
  { id: "faq", label: "FAQ" },
] as const;

export interface FxState {
  /** The "how it works" step nearest the middle of the viewport. */
  step: number;
  /** The rail's current section id. */
  section: string;
  /** Whether the call-to-action dock is showing. */
  dock: boolean;
}

const REVEAL = ".land-sec > .sec-head, .land-sec .card:not(.step), .land-sec .tw, .land-sec .note, .land-stats .stat, .land-cta";

const reducedMotion = () => globalThis.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false;

/** Smooth-scroll to a rail section ("top" or a section id). */
export function scrollToSection(id: string) {
  const behavior: ScrollBehavior = reducedMotion() ? "auto" : "smooth";
  if (id === "top") {
    window.scrollTo({ top: 0, behavior });
    return;
  }
  document.getElementById(id)?.scrollIntoView({ behavior, block: "start" });
}

function countUp(v: HTMLElement) {
  const node = v.firstChild;
  if (node?.nodeType !== Node.TEXT_NODE || v.dataset.counted) return;
  const to = Number(node.nodeValue);
  const from = Number(v.dataset.countFrom);
  if (!Number.isFinite(to) || !Number.isFinite(from)) return;
  v.dataset.counted = "1";
  const t0 = performance.now();
  node.nodeValue = String(from);
  const step = (t: number) => {
    const k = Math.min(1, (t - t0) / 1200);
    node.nodeValue = String(Math.round(from + (to - from) * (1 - (1 - k) ** 3)));
    if (k < 1) requestAnimationFrame(step);
  };
  requestAnimationFrame(step);
}

export function useLandingFx(): FxState {
  const [state, setState] = useState<FxState>({ step: 0, section: "top", dock: false });

  useEffect(() => {
    const root = document.querySelector<HTMLElement>(".shell.land");
    if (!root) return;
    const motion = !reducedMotion();
    const tagged: HTMLElement[] = [];
    const timers: number[] = [];
    let io: IntersectionObserver | undefined;
    let raf = 0;

    const set = (p: Partial<FxState>) =>
      setState((s) => {
        const n = { ...s, ...p };
        return n.step === s.step && n.section === s.section && n.dock === s.dock ? s : n;
      });

    const show = (el: HTMLElement) => {
      if (!el.isConnected || el.classList.contains("in")) return;
      el.classList.add("in");
      el.querySelectorAll<HTMLElement>("[data-count-from]").forEach(countUp);
      const delay = Number.parseInt(el.style.getPropertyValue("--d"), 10) || 0;
      // When the entrance is over, hand the element back to its normal styles (hover lift, transitions).
      timers.push(
        window.setTimeout(() => {
          el.classList.remove("rv", "in");
          el.style.removeProperty("--d");
        }, 1900 + delay),
      );
    };

    if (motion && typeof IntersectionObserver !== "undefined") {
      io = new IntersectionObserver(
        (entries) => {
          for (const en of entries) {
            if (!en.isIntersecting) continue;
            io?.unobserve(en.target);
            show(en.target as HTMLElement);
          }
        },
        { rootMargin: "0px 0px -8% 0px", threshold: 0.08 },
      );
      document.documentElement.classList.add("fx");
      const vh = window.innerHeight;
      for (const el of root.querySelectorAll<HTMLElement>(REVEAL)) {
        const sibs = Array.from(el.parentElement?.children ?? []).filter((c) => c.matches(REVEAL));
        el.style.setProperty("--d", `${Math.min(sibs.indexOf(el), 5) * 80}ms`);
        el.classList.add("rv");
        tagged.push(el);
        if (el.getBoundingClientRect().top < vh * 0.9) requestAnimationFrame(() => show(el));
        else io.observe(el);
      }
    }

    const frame = () => {
      raf = 0;
      const y = window.scrollY || 0;
      const vh = window.innerHeight;
      const max = Math.max(1, document.documentElement.scrollHeight - vh);
      root.querySelector<HTMLElement>(".fx-progress")?.style.setProperty("--fx-p", Math.min(1, y / max).toFixed(4));
      document.querySelector(".sitenav")?.classList.toggle("scrolled", y > 24);
      if (motion) root.querySelector<HTMLElement>(".hero-aura")?.style.setProperty("--py", String(Math.round(Math.min(y, 900) * 0.22)));

      // Dock: once the hero has scrolled away, and not while the closing call-to-action is in view.
      const heroBottom = root.querySelector(".hero")?.getBoundingClientRect().bottom ?? 0;
      const ctaTop = root.querySelector(".land-cta")?.getBoundingClientRect().top ?? Number.POSITIVE_INFINITY;
      const dock = heroBottom < 40 && ctaTop > vh * 0.85;

      // Rail: the last section whose top has crossed the upper part of the viewport.
      let section: string = landingSections[0].id;
      for (const s of landingSections) {
        const el = s.id === "top" ? null : document.getElementById(s.id);
        if (el && el.getBoundingClientRect().top <= vh * 0.4) section = s.id;
      }

      // Pinned panel: mirror the step nearest the middle of the viewport.
      let step = 0;
      let best = Number.POSITIVE_INFINITY;
      root.querySelectorAll(".scrolly .step").forEach((el, i) => {
        const r = el.getBoundingClientRect();
        const d = Math.abs((r.top + r.bottom) / 2 - vh * 0.46);
        if (d < best) {
          best = d;
          step = i;
        }
      });
      set({ dock, section, step });
    };
    const onScroll = () => {
      if (!raf) raf = requestAnimationFrame(frame);
    };
    const onMove = (e: PointerEvent) => {
      const card = (e.target as Element | null)?.closest?.<HTMLElement>(".land article.card");
      if (!card) return;
      const r = card.getBoundingClientRect();
      card.style.setProperty("--mx", `${Math.round(e.clientX - r.left)}px`);
      card.style.setProperty("--my", `${Math.round(e.clientY - r.top)}px`);
    };

    window.addEventListener("scroll", onScroll, { passive: true });
    window.addEventListener("resize", onScroll);
    document.addEventListener("pointermove", onMove, { passive: true });
    frame();

    return () => {
      window.removeEventListener("scroll", onScroll);
      window.removeEventListener("resize", onScroll);
      document.removeEventListener("pointermove", onMove);
      io?.disconnect();
      if (raf) cancelAnimationFrame(raf);
      for (const t of timers) clearTimeout(t);
      for (const el of tagged) {
        el.classList.remove("rv", "in");
        el.style.removeProperty("--d");
      }
      document.documentElement.classList.remove("fx");
      document.querySelector(".sitenav")?.classList.remove("scrolled");
    };
  }, []);

  return state;
}
