import { InstancePhase, Role } from "@gen/kondukter/api/v1/types_pb";
import { Fragment, type CSSProperties, useEffect, useRef, useState } from "react";
import { Link } from "react-router";
import { useMe } from "../auth/AuthProvider";
import { homePath } from "../auth/roles";
import { landingSections, scrollToSection, useLandingFx } from "../lib/landingFx";
import { Logo } from "../components/Logo";
import { Pipeline } from "../components/Pipeline";
import { SiteNav } from "../components/SiteNav";
import { Stat } from "../components/Stat";
import { Terminal } from "../components/Terminal";

/** The guest home page: what Kondukter is, how it works and what it offers, with a way in. */

const lifecycle = [InstancePhase.PENDING, InstancePhase.SCHEDULED, InstancePhase.PROVISIONING, InstancePhase.RUNNING];

/** A looping preview of an instance coming up; it rests on RUNNING when the visitor prefers less motion. */
function HeroDemo() {
  const [step, setStep] = useState(lifecycle.length - 1);
  useEffect(() => {
    if (globalThis.matchMedia?.("(prefers-reduced-motion: reduce)").matches) return;
    let i = 0;
    setStep(0);
    const t = setInterval(() => {
      i = (i + 1) % (lifecycle.length + 2); // two extra ticks to rest on RUNNING
      setStep(Math.min(i, lifecycle.length - 1));
    }, 1500);
    return () => clearInterval(t);
  }, []);
  const phase = lifecycle[step] ?? InstancePhase.RUNNING;
  return (
    <div className="card reg glow demo">
      <div className="row">
        <span className="lbl">create instance · gpu-gtx1060-1x</span>
        <span className="sp" />
        <span className="m mut2">live</span>
      </div>
      <Pipeline phase={phase} />
      {phase === InstancePhase.RUNNING ? (
        <Terminal command="ssh -p 40001 ubuntu@100.64.0.5" />
      ) : (
        <div className="term">
          <header>
            <span className="lbl">ssh</span>
          </header>
          <pre>
            <code className="mut2">waiting for the node…</code>
          </pre>
        </div>
      )}
    </div>
  );
}

const what = [
  { n: "01", t: "Partners lend idle capacity", d: "Datacenters, labs and universities dedicate a fixed share of machines that would otherwise sit idle. They install one small agent; nothing opens inbound." },
  { n: "02", t: "Warden schedules it", d: "A control plane knows every node, GPU and free core. It places each request on the best-fit machine and keeps the books straight in one database." },
  { n: "03", t: "Customers get a real VM", d: "Pick a shape and an image, add an SSH key, and a few seconds later you log in to a whole virtual machine, with a whole GPU when you ask for one." },
];

const how = [
  { t: "Agents dial out", d: "Every node keeps one outbound, mutually authenticated stream to the control plane. No inbound ports, no VPN per partner." },
  { t: "Desired state, reconciled", d: "The control plane says what should run; the agent converges to it. If the link drops, running VMs keep running and nothing is deleted." },
  { t: "Whole-GPU passthrough", d: "A GPU is handed to exactly one tenant through VFIO, so the guest runs NVIDIA's own driver at full speed. No sharing, no slicing." },
  { t: "Direct access", d: "You connect straight to the node. Customer traffic never flows through Warden, and an outage there never takes your session down." },
];

/** What the pinned panel shows while each of the four steps above scrolls past. */
const scenes: { stepLabel: string; title: string; lines: { prompt?: boolean; text: string; tone?: "d" | "ok" }[] }[] = [
  { stepLabel: "Dial out", title: "agent · dial out", lines: [{ prompt: true, text: "warden-agent --server control.warden" }, { text: "dialing out :443 · mutual TLS", tone: "d" }, { text: "✓ stream open · inbound ports: 0", tone: "ok" }] },
  { stepLabel: "Reconcile", title: "agent · reconcile", lines: [{ text: "control plane unreachable · 12 s", tone: "d" }, { text: "running VMs: 3 · deleted: 0" }, { text: "✓ reconnected · desired gen 42 = observed 42", tone: "ok" }] },
  { stepLabel: "Passthrough", title: "node · gpu passthrough", lines: [{ prompt: true, text: "vfio-pci bind 0000:01:00.0" }, { text: "gpu 0 → i-7c1e (exclusive)" }, { text: "✓ guest sees GeForce GTX 1060 6GB", tone: "ok" }] },
  { stepLabel: "Direct", title: "you · direct access", lines: [{ prompt: true, text: "ssh -p 40001 ubuntu@100.64.0.5" }, { text: "bytes carried by Warden: 0", tone: "d" }, { text: "✓ session open · node ⇄ you", tone: "ok" }] },
];

const plans = [
  {
    id: "cpu-2x2", kind: "cpu", featured: false, blurb: "A small, always-ready Linux box for services, bots and build jobs.",
    specs: ["No accelerator", "2 vCPU · 2 GiB RAM · 20 GiB disk", "Ubuntu 24.04 · arm64 · amd64", "Curated, ready-to-use image", "Dedicated capacity · direct SSH"],
  },
  {
    id: "gpu-gtx1060-1x", kind: "gpu", featured: true, blurb: "Everyday GPU work: CUDA development, small-model inference and rendering.",
    specs: ["GeForce GTX 1060 6GB × 1", "4 vCPU · 8 GiB RAM · 40 GiB disk", "Ubuntu 24.04 · amd64", "NVIDIA driver and CUDA pre-installed", "Dedicated capacity · direct SSH"],
  },
];

const examples = [
  { label: "Smoke test", span: "10 minutes", minutes: 10 },
  { label: "Overnight run", span: "8 hours", minutes: 480 },
  { label: "A full week", span: "168 hours", minutes: 10080 },
];

type IconName = "clock" | "gpu" | "term" | "disk" | "code" | "key" | "cuda" | "model" | "film" | "book";
const iconPaths: Record<IconName, string> = {
  clock: "M12 3.5a8.5 8.5 0 100 17 8.5 8.5 0 000-17zM12 7.5V12l3 2",
  gpu: "M4 7h16a1 1 0 011 1v8a1 1 0 01-1 1H4a1 1 0 01-1-1V8a1 1 0 011-1zM9 9.8a2.2 2.2 0 100 4.4 2.2 2.2 0 000-4.4zM14 10h4M14 14h4M6 17v2M10 17v2",
  term: "M4 5h16a1 1 0 011 1v12a1 1 0 01-1 1H4a1 1 0 01-1-1V6a1 1 0 011-1zM7 10l3 2.5L7 15M12.5 15H17",
  disk: "M12 4c3.9 0 7 1.3 7 3s-3.1 3-7 3-7-1.3-7-3 3.1-3 7-3zM5 7v10c0 1.7 3.1 3 7 3s7-1.3 7-3V7M5 12c0 1.7 3.1 3 7 3s7-1.3 7-3",
  code: "M9 5C6.8 5 6.5 6 6.5 8v1.5c0 1.3-.8 2.2-2.5 2.5 1.7.3 2.5 1.2 2.5 2.5V16c0 2 .3 3 2.5 3M15 5c2.2 0 2.5 1 2.5 3v1.5c0 1.3.8 2.2 2.5 2.5-1.7.3-2.5 1.2-2.5 2.5V16c0 2-.3 3-2.5 3",
  key: "M8 8.5a3.5 3.5 0 100 7 3.5 3.5 0 000-7zM11.5 12H21M17 12v3M20 12v2.5",
  cuda: "M7 6h10a1 1 0 011 1v10a1 1 0 01-1 1H7a1 1 0 01-1-1V7a1 1 0 011-1zM9 3v3M15 3v3M9 18v3M15 18v3M3 9h3M3 15h3M18 9h3M18 15h3",
  model: "M6 10a2 2 0 100 4 2 2 0 000-4zM18 4a2 2 0 100 4 2 2 0 000-4zM18 16a2 2 0 100 4 2 2 0 000-4zM8 12h4l4-5M12 12l4 5",
  film: "M5 5h14a1 1 0 011 1v12a1 1 0 01-1 1H5a1 1 0 01-1-1V6a1 1 0 011-1zM4 9h16M4 15h16M8 5v14M16 5v14",
  book: "M5 5h9a3 3 0 013 3v11H8a3 3 0 01-3-3V5zM9 9h5M9 12.5h5",
};

function Icon({ name }: { name: IconName }) {
  return (
    <span className="ico" aria-hidden="true">
      <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round">
        <path d={iconPaths[name]} />
      </svg>
    </span>
  );
}

const included: { icon: IconName; t: string; d: string; tag: string }[] = [
  { icon: "clock", t: "Per-minute billing", d: "You pay for the minutes an instance runs. Stop it after ten minutes and you pay for ten, with a live cost estimate on every instance.", tag: "Billing" },
  { icon: "gpu", t: "A whole GPU, yours alone", d: "The card is handed to exactly one tenant through VFIO passthrough. NVIDIA's own driver, full speed, no sharing and no slicing.", tag: "Isolation" },
  { icon: "term", t: "Direct SSH", d: "Copy one command and connect straight to the node. Your traffic never flows through Warden, so there is no bandwidth to mark up.", tag: "Access" },
  { icon: "disk", t: "Curated images", d: "Ubuntu 24.04 on amd64 and arm64, with the NVIDIA driver and CUDA pre-installed on GPU shapes. Ready at the first login.", tag: "Images" },
  { icon: "code", t: "Cloud-init and open ports", d: "Bring your own user-data, up to 16 KiB, and expose extra TCP or UDP ports at launch. 22/tcp is always there.", tag: "Automation" },
  { icon: "key", t: "API keys and kctl", d: "Create keys in the console and drive everything from kctl or your own tools. A key is shown once and can be revoked at any time.", tag: "API" },
];

const useCases: { icon: IconName; t: string; d: string; tag: string }[] = [
  { icon: "cuda", t: "CUDA development and CI", d: "A clean GPU machine to build and test CUDA code on Pascal, billed only while the job runs.", tag: "CUDA 12 · sm_61" },
  { icon: "model", t: "Small-model inference", d: "Serve quantized models and embeddings that fit in 6 GB of VRAM, on a port you choose at launch.", tag: "6 GB VRAM" },
  { icon: "film", t: "Video transcode and render", d: "Offload hardware H.264 and HEVC encodes and GPU-accelerated renders without owning the card.", tag: "NVENC" },
  { icon: "book", t: "Notebooks and data prep", d: "Log in to a ready Ubuntu box, run Jupyter or your scripts, terminate when you are done.", tag: "Ubuntu 24.04" },
];

const gpuSpecs: [string, string][] = [
  ["GPU", "GeForce GTX 1060 6GB × 1"],
  ["Architecture", "Pascal · 1280 CUDA cores"],
  ["Memory", "6 GB GDDR5"],
  ["Compute", "4 vCPU · 8 GiB RAM · 40 GiB disk"],
  ["Image", "Ubuntu 24.04 · NVIDIA driver · CUDA 12"],
  ["Capacity", "Dedicated, one tenant per GPU"],
];

const faq = [
  { q: "How does billing work?", a: "Instances are billed by the minute at the hourly price of their shape, so a GTX 1060 costs 0.67 cents a minute. You top up your balance with €10, €25, €50 or €100, and Billing shows your balance, daily burn, runway and an invoice per month." },
  { q: "What do I get when I launch an instance?", a: "A whole virtual machine on a dedicated slot: Ubuntu 24.04, your SSH keys, the ports you choose and optional cloud-init user-data. GPU shapes come with the NVIDIA driver and CUDA already installed." },
  { q: "How do I connect?", a: "Open the instance, copy the ssh command and run it. You connect straight to the node, and the page lists the host, the SSH port and a mapping for every exposed port." },
  { q: "Is the GPU shared with anyone?", a: "No. A GPU is passed through to exactly one tenant with VFIO, so the guest runs NVIDIA's own driver at full speed. There is no GPU sharing and no slicing." },
  { q: "What happens if a node goes down?", a: "Every node keeps one outbound stream to the control plane, and a lost node is noticed in 45 seconds. If only the link drops, running VMs keep running and nothing is deleted. You see the phase change live on your instance." },
  { q: "Can I automate it?", a: "Yes. Create an API key under Keys and use kctl or your own tools to list offers, launch and terminate instances. Every privileged action on the platform is recorded in an audit log." },
  { q: "How do partners get paid?", a: "Partners lend idle machines and see earnings per day and per node, utilization and payouts in their own area. They never see customer data." },
  { q: "Which GPU can I rent?", a: "The GeForce GTX 1060 6GB: a whole card for you alone, from €0.40 an hour. CPU-only machines start at €0.05 an hour." },
];

const offers = [
  { name: "cpu-2x2", gpu: "", spec: "2 vCPU · 2 GiB · 20 GiB", arch: "arm64 · amd64", price: "€0.05" },
  { name: "gpu-gtx1060-1x", gpu: "GeForce GTX 1060 6GB × 1", spec: "4 vCPU · 8 GiB · 40 GiB", arch: "amd64", price: "€0.40" },
];

const audiences = [
  { who: "Customers", d: "Browse offers, create an instance, watch it come up live, copy the ssh command, and see running time and estimated cost.", items: ["Offers and filters", "Live lifecycle", "SSH and API keys"] },
  { who: "Partners", d: "See your sites and nodes, enroll a machine with a one-time command, drain it for maintenance, and read utilization. Never customer data.", items: ["Sites and nodes", "One-time enrollment", "Drain and undrain"] },
  { who: "Operators", d: "Watch the whole fleet, quarantine a node, force-terminate an instance and read the audit log. Every privileged action is recorded.", items: ["Fleet map and stage view", "Quarantine", "Audit log"] },
];


const rates = [
  { id: "cpu-2x2", label: "cpu-2x2", note: "2 vCPU · 2 GiB", centsPerHour: 5 },
  { id: "gpu-gtx1060-1x", label: "gpu-gtx1060-1x", note: "GTX 1060 6GB · 4 vCPU", centsPerHour: 40 },
];

const euro = (cents: number) => new Intl.NumberFormat("en", { style: "currency", currency: "EUR" }).format(cents / 100);

const cheap = [
  { t: "Idle hardware, already paid for", d: "Partners lend machines that would otherwise sit unused. Capacity that costs them almost nothing extra costs you pennies." },
  { t: "Billed by the minute", d: "You pay for the minutes an instance runs, not for a rounded-up hour. Stop it after ten minutes and you pay for ten." },
  { t: "No middle layer", d: "Your traffic goes straight to the node. We never carry it, so there is no bandwidth to mark up." },
];

/** What a run costs at the demo prices, so "cheap" is a number the visitor can move. */
function CostCalculator() {
  const [shape, setShape] = useState(rates[1]?.id ?? "");
  const [hours, setHours] = useState(48);
  const rate = rates.find((r) => r.id === shape) ?? rates[0];
  const cents = (rate?.centsPerHour ?? 0) * hours;
  const shown = useTweened(cents);
  return (
    <div className="card reg glow calc">
      <div className="seg" role="group" aria-label="Shape">
        {rates.map((r) => (
          <button key={r.id} type="button" aria-pressed={shape === r.id} onClick={() => setShape(r.id)}>
            {r.label}
          </button>
        ))}
      </div>
      <div className="fld">
        <label htmlFor="calc-hours">
          Run it for <span className="m ink-h">{hours} h</span>
        </label>
        <input id="calc-hours" type="range" min={1} max={168} value={hours} onChange={(e) => setHours(Number(e.currentTarget.value))} />
      </div>
      <div>
        <span className="lbl">That costs</span>
        <div className="calc-total" aria-hidden="true" data-testid="calc-total">
          {euro(shown)}
        </div>
        <span className="sr" aria-live="polite">
          {euro(cents)}
        </span>
        <p className="mut m">
          {rate?.note} · {euro(rate?.centsPerHour ?? 0)}/hr · {(((rate?.centsPerHour ?? 0) / 60)).toFixed(2)} cents a minute
        </p>
      </div>
    </div>
  );
}

const heroWords = ["Idle", "capacity,", "ready", "in", "seconds."];

/** A number that eases to its new value, or jumps when the visitor prefers less motion. */
function useTweened(target: number, ms = 460) {
  const [shown, setShown] = useState(target);
  const from = useRef(target);
  useEffect(() => {
    const animate = typeof requestAnimationFrame === "function" && globalThis.matchMedia && !globalThis.matchMedia("(prefers-reduced-motion: reduce)").matches;
    if (!animate) {
      from.current = target;
      setShown(target);
      return;
    }
    const start = from.current;
    const t0 = performance.now();
    let raf = 0;
    const tick = (t: number) => {
      const k = Math.min(1, (t - t0) / ms);
      from.current = start + (target - start) * (1 - (1 - k) ** 3);
      setShown(from.current);
      if (k < 1) raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, [target, ms]);
  return shown;
}

/** Where a "launch" button goes: log in first, the offers for a customer, the product list for staff (who do not rent). */
function Launch({ className, guest, customer }: { className: string; guest: string; customer: string }) {
  const auth = useMe();
  if (auth.status !== "signedIn") {
    return (
      <Link className={className} to="/login">
        {guest}
      </Link>
    );
  }
  return auth.me.role === Role.CUSTOMER ? (
    <Link className={className} to="/app/offers">
      {customer}
    </Link>
  ) : (
    <Link className={className} to="/products">
      See product
    </Link>
  );
}

/** "How it works" with a panel that stays on screen while the four steps scroll past; the step nearest the middle drives it. */
function HowItWorks({ step }: { step: number }) {
  return (
    <div className="scrolly">
      <aside className="pin" aria-hidden="true">
        <div className="card reg glow pin-card">
          <div className="row">
            <span className="lbl">How a node comes online</span>
            <span className="sp" />
            <span className="m mut2">
              {String(step + 1).padStart(2, "0")} / {String(how.length).padStart(2, "0")}
            </span>
          </div>
          <div className="pin-track">
            <span className="pin-fill" style={{ "--sp": step / Math.max(1, how.length - 1) } as CSSProperties} />
            {scenes.map((s, i) => (
              <span key={s.stepLabel} className={`pin-st${i < step ? " done" : ""}${i === step ? " now" : ""}`}>
                <i className="nd" />
                <span>{s.stepLabel}</span>
              </span>
            ))}
          </div>
          <div className="term">
            <header>
              <span className="lbl">{scenes[step]?.title}</span>
              <span className="acts">
                <span className="chip s-run">
                  <span className="led" />
                  live
                </span>
              </span>
            </header>
            <div className="pin-stage">
              {scenes.map((s, i) => (
                <div key={s.stepLabel} className={`pin-scene${i === step ? " on" : ""}`}>
                  {s.lines.map((l, n) => (
                    <span key={l.text} style={{ "--n": n } as CSSProperties}>
                      {l.prompt && <b className="k">$ </b>}
                      {l.tone ? <i className={l.tone}>{l.text}</i> : l.text}
                    </span>
                  ))}
                </div>
              ))}
            </div>
          </div>
        </div>
      </aside>
      <div className="steps">
        {how.map((h, i) => (
          <article key={h.t} className={`card col step${i === step ? " active" : ""}`}>
            <span className="m mut2">{String(i + 1).padStart(2, "0")}</span>
            <h3>{h.t}</h3>
            <p className="mut">{h.d}</p>
          </article>
        ))}
      </div>
    </div>
  );
}

function AuthButton({ large = false }: { large?: boolean }) {
  const auth = useMe();
  const size = large ? " lg" : "";
  if (auth.status === "signedIn") {
    return (
      <Link className={`btn p${size}`} to={homePath(auth.me.role)}>
        Open dashboard
      </Link>
    );
  }
  return (
    <Link className={`btn p${size}`} to="/login">
      Log in
    </Link>
  );
}

export function LandingPage() {
  const fx = useLandingFx();
  return (
    <div className="shell land">
      <div className="banner" role="note">
        <span className="chip s-run">
          <span className="led" aria-hidden="true" />
          cheap by design
        </span>
        <span>GPUs from €0.40 an hour · CPUs from €0.05 an hour · billed by the minute</span>
        <Link to="/products">See prices →</Link>
      </div>
      <div className="fx-progress" aria-hidden="true">
        <i />
      </div>
      <nav className="fx-rail" aria-label="Page sections">
        {landingSections.map((s) => (
          <button key={s.id} type="button" className={fx.section === s.id ? "on" : undefined} aria-current={fx.section === s.id ? "true" : undefined} onClick={() => scrollToSection(s.id)}>
            <span className="t">{s.label}</span>
            <i />
          </button>
        ))}
      </nav>
      <aside className={`fx-dock${fx.dock ? " show" : ""}`} aria-label="Get started">
        <span className="chip s-run">
          <span className="led" aria-hidden="true" />
          from €0.05/hr
        </span>
        <span className="dock-txt">Whole GPUs by the minute. Zero inbound ports.</span>
        <AuthButton />
      </aside>
      <SiteNav />

      <main>
        <section className="hero">
          <div className="hero-aura" aria-hidden="true" />
          <div className="hero-copy">
            <span className="lbl">Compute, on demand</span>
            <h1>
              {heroWords.map((w, i) => (
                <Fragment key={w}>
                  {i > 0 && " "}
                  <span className="w">
                    <span style={{ "--i": i } as CSSProperties}>{w}</span>
                  </span>
                </Fragment>
              ))}
            </h1>
            <p className="lead">
              Warden turns spare datacenter machines, GPUs included, into SSH-ready virtual machines. Partners put idle hardware to work; customers rent a whole
              GPU by the minute.
            </p>
            <div className="row" style={{ gap: 8 }}>
              <span className="chip s-run">from €0.05/hr · cpu</span>
              <span className="chip gpu">gtx 1060 · €0.40/hr · available now</span>
            </div>
            <div className="row">
              <AuthButton large />
              <Link className="btn lg" to="/products">
                Browse products
              </Link>
            </div>
          </div>
          <HeroDemo />
        </section>

        <div className="land-stats">
          <Stat label="Inbound ports opened" value="0" from={8} />
          <Stat label="GPU per tenant" value="1" unit="whole card" />
          <Stat label="Lost node noticed in" value="45" unit="seconds" from={0} />
        </div>

        <section id="plans" className="land-sec">
          <div className="sec-head">
            <span className="lbl">Pricing</span>
            <h2>Simple prices. Real hardware.</h2>
          </div>
          <div className="plans">
            {plans.map((p) => {
              const cph = rates.find((r) => r.id === p.id)?.centsPerHour ?? 0;
              return (
                <article key={p.id} className="card reg col plan" data-featured={p.featured}>
                  <div className="row">
                    <span className={p.kind === "gpu" ? "chip gpu" : "chip"}>{p.kind}</span>
                    <span className="sp" />
                    <span className="chip s-run">
                      <span className="led" aria-hidden="true" />
                      {p.featured ? "Most popular" : "Available now"}
                    </span>
                  </div>
                  <div className="col" style={{ gap: 6 }}>
                    <h3 className="m">{p.id}</h3>
                    <p className="mut plan-blurb">{p.blurb}</p>
                  </div>
                  <div className="col" style={{ gap: 4 }}>
                    <div className="plan-price">
                      {euro(cph)}
                      <small>/ hour</small>
                    </div>
                    <span className="mut2 m">
                      {euro(cph * 24)}/day · {euro(cph * 730)}/mo
                    </span>
                  </div>
                  <ul className="ticks plan-specs">
                    {p.specs.map((s) => (
                      <li key={s}>{s}</li>
                    ))}
                  </ul>
                  <div className="plan-cta">
                    <Launch className={p.featured ? "btn p lg" : "btn lg"} guest={p.featured ? "Start with this GPU" : "Get started"} customer="Launch" />
                  </div>
                </article>
              );
            })}
          </div>
          <p className="note">Prices are per hour, billed by the minute, in euro. Every shape is dedicated capacity: one tenant per machine slot, one tenant per GPU.</p>
          <div className="eg-head">
            <span className="lbl">What a GTX 1060 costs in practice</span>
          </div>
          <div className="g3">
            {examples.map((e) => (
              <article key={e.label} className="card reg col eg">
                <span className="lbl">{e.label}</span>
                <div className="eg-price">{euro(((rates[1]?.centsPerHour ?? 0) * e.minutes) / 60)}</div>
                <span className="mut2 m">
                  {e.span} on gpu-gtx1060-1x
                </span>
              </article>
            ))}
          </div>
        </section>

        <section id="cheap" className="land-sec">
          <div className="sec-head">
            <span className="lbl">Cheap by design</span>
            <h2>Pennies an hour. Seriously.</h2>
          </div>
          <div className="cheap-grid">
            <CostCalculator />
            <div className="col">
              {cheap.map((c) => (
                <article key={c.t} className="card col">
                  <h3>{c.t}</h3>
                  <p className="mut">{c.d}</p>
                </article>
              ))}
            </div>
          </div>
          <p className="note">
            Prices are per hour and billed by the minute. New accounts start with credit: log in and open Billing to see your balance, its history and an invoice.
          </p>
        </section>

        <section id="what" className="land-sec">
          <div className="sec-head">
            <span className="lbl">What we do</span>
            <h2>Hardware that sat idle, put to work.</h2>
          </div>
          <div className="g3">
            {what.map((w) => (
              <article key={w.n} className="card reg col">
                <span className="m acc">{w.n}</span>
                <h3>{w.t}</h3>
                <p className="mut">{w.d}</p>
              </article>
            ))}
          </div>
        </section>

        <section id="how" className="land-sec">
          <div className="sec-head">
            <span className="lbl">How it works</span>
            <h2>Calm by default, even when things break.</h2>
          </div>
          <HowItWorks step={fx.step} />
        </section>

        <section id="included" className="land-sec">
          <div className="sec-head">
            <span className="lbl">Included with every instance</span>
            <h2>Everything you need to start, nothing to bolt on.</h2>
          </div>
          <div className="g3">
            {included.map((f) => (
              <article key={f.t} className="card reg col feat">
                <Icon name={f.icon} />
                <h3>{f.t}</h3>
                <p className="mut">{f.d}</p>
                <span className="pill">{f.tag}</span>
              </article>
            ))}
          </div>
        </section>

        <section id="usecases" className="land-sec">
          <div className="sec-head">
            <span className="lbl">GeForce GTX 1060 6GB</span>
            <h2>The everyday GPU, ready now.</h2>
          </div>
          <div className="uc">
            <article className="card reg glow col spec">
              <div className="row">
                <span className="chip gpu">gpu-gtx1060-1x</span>
                <span className="sp" />
                <span className="chip s-run">
                  <span className="led" aria-hidden="true" />
                  Available now
                </span>
              </div>
              <div className="col" style={{ gap: 4 }}>
                <div className="plan-price">
                  {euro(rates[1]?.centsPerHour ?? 0)}
                  <small>/ hour</small>
                </div>
                <span className="mut2 m">
                  {euro((rates[1]?.centsPerHour ?? 0) * 24)}/day · {euro((rates[1]?.centsPerHour ?? 0) * 730)}/mo · billed by the minute
                </span>
              </div>
              <dl className="specs">
                {gpuSpecs.map(([k, v]) => (
                  <Fragment key={k}>
                    <dt>{k}</dt>
                    <dd>{v}</dd>
                  </Fragment>
                ))}
              </dl>
              <div className="plan-cta">
                <Launch className="btn p lg" guest="Log in to launch" customer="Launch a GTX 1060" />
              </div>
            </article>
            <div className="uc-grid">
              {useCases.map((u) => (
                <article key={u.t} className="card reg col feat">
                  <Icon name={u.icon} />
                  <h3>{u.t}</h3>
                  <p className="mut">{u.d}</p>
                  <span className="pill">{u.tag}</span>
                </article>
              ))}
            </div>
          </div>
        </section>

        <section id="offer" className="land-sec">
          <div className="sec-head">
            <span className="lbl">What we offer</span>
            <h2>A small, honest catalog.</h2>
          </div>
          <div className="tw">
            <table className="tbl" style={{ minWidth: 640 }}>
              <caption>Catalog, dedicated capacity</caption>
              <thead>
                <tr>
                  <th scope="col">Shape</th>
                  <th scope="col">Accelerator</th>
                  <th scope="col">Size</th>
                  <th scope="col">Architecture</th>
                  <th scope="col" className="num">
                    Price / hour
                  </th>
                </tr>
              </thead>
              <tbody>
                {offers.map((o) => (
                  <tr key={o.name}>
                    <td className="m ink-h">{o.name}</td>
                    <td>{o.gpu ? <span className="chip gpu">{o.gpu}</span> : "none"}</td>
                    <td className="m">{o.spec}</td>
                    <td className="m">{o.arch}</td>
                    <td className="m ink-h num">{o.price}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <p className="note">
            Curated Ubuntu 24.04 images, with the NVIDIA driver and CUDA pre-installed on GPU shapes.
          </p>
          <div className="row">
            <Link className="btn" to="/products">
              Compare all products
            </Link>
          </div>
        </section>

        <section id="who" className="land-sec">
          <div className="sec-head">
            <span className="lbl">Who it is for</span>
            <h2>One platform, three views.</h2>
          </div>
          <div className="g3">
            {audiences.map((a) => (
              <article key={a.who} className="card reg col">
                <h3>{a.who}</h3>
                <p className="mut">{a.d}</p>
                <ul className="ticks">
                  {a.items.map((i) => (
                    <li key={i}>{i}</li>
                  ))}
                </ul>
              </article>
            ))}
          </div>
        </section>

        <section id="faq" className="land-sec">
          <div className="sec-head">
            <span className="lbl">Questions</span>
            <h2>Answers before you ask.</h2>
          </div>
          <div className="faq">
            {faq.map((f) => (
              <details key={f.q} className="card reg">
                <summary>{f.q}</summary>
                <p className="ans">{f.a}</p>
              </details>
            ))}
          </div>
        </section>

        <section className="land-cta card reg glow">
          <h2>Ready to look around?</h2>
          <p className="mut">Log in to see your balance, your instances or your nodes.</p>
          <div className="row" style={{ justifyContent: "center" }}>
            <AuthButton large />
          </div>
        </section>
      </main>

      <footer className="land-foot">
        <Logo size={22} />
        <span className="mut2 m">© {new Date().getFullYear()} Warden</span>
      </footer>
    </div>
  );
}
