import { InstancePhase } from "@gen/kondukter/api/v1/types_pb";
import { useEffect, useState } from "react";
import { Link } from "react-router";
import { useMe } from "../auth/AuthProvider";
import { homePath } from "../auth/roles";
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
        <div className="calc-total" aria-live="polite" data-testid="calc-total">
          {euro(cents)}
        </div>
        <p className="mut m">
          {rate?.note} · {euro(rate?.centsPerHour ?? 0)}/hr · {(((rate?.centsPerHour ?? 0) / 60)).toFixed(2)} cents a minute
        </p>
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
      <SiteNav />

      <main>
        <section className="hero">
          <div className="hero-copy">
            <span className="lbl">Compute, on demand</span>
            <h1>Idle capacity, ready in seconds.</h1>
            <p className="lead">
              Warden turns spare datacenter machines, GPUs included, into SSH-ready virtual machines. Partners put idle hardware to work; customers rent a whole
              GPU by the minute.
            </p>
            <div className="row" style={{ gap: 8 }}>
              <span className="chip s-run">from €0.05/hr · cpu</span>
              <span className="chip gpu">from €0.40/hr · whole gpu</span>
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
          <Stat label="Inbound ports opened" value="0" />
          <Stat label="GPU per tenant" value="1" unit="whole card" />
          <Stat label="Lost node noticed in" value="45" unit="seconds" />
        </div>

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
          <div className="grid2">
            {how.map((h, i) => (
              <article key={h.t} className="card col">
                <span className="m mut2">{String(i + 1).padStart(2, "0")}</span>
                <h3>{h.t}</h3>
                <p className="mut">{h.d}</p>
              </article>
            ))}
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
