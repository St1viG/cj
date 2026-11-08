import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { ShapeSchema } from "@gen/kondukter/api/v1/catalog_pb";
import { CapacityClass, InstancePhase, MoneySchema, Protocol } from "@gen/kondukter/api/v1/types_pb";
import { renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { copyText } from "./clipboard";
import {
  canTerminate,
  capacityLabel,
  formatAccelerator,
  formatMinutes,
  formatMiB,
  formatMoney,
  formatTimestamp,
  isTransitional,
  phaseLabel,
  protocolLabel,
  shortId,
} from "./format";
import { newId, useIdempotencyKey } from "./ids";
import { parsePorts } from "./ports";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("parsePorts", () => {
  it("reads numbers with optional protocols, separated by commas, spaces or lines", () => {
    const ports = parsePorts("8080, 53/udp\n443/TCP  9000");
    expect(ports.map((p) => [p.guestPort, p.protocol])).toEqual([
      [8080, Protocol.TCP],
      [53, Protocol.UDP],
      [443, Protocol.TCP],
      [9000, Protocol.TCP],
    ]);
  });

  it("accepts nothing", () => {
    expect(parsePorts("")).toEqual([]);
    expect(parsePorts(" \n ,, ")).toEqual([]);
  });

  it.each([
    ["web", /"web" is not a port/],
    ["8080/sctp", /not a port/],
    ["0", /between 1 and 65535/],
    ["65536", /between 1 and 65535/],
    ["22", /22\/tcp is always exposed/],
    ["22/tcp", /22\/tcp is always exposed/],
    ["8080, 8080/tcp", /8080\/tcp is listed twice/],
    [Array.from({ length: 17 }, (_, i) => 1000 + i).join(","), /At most 16/],
  ])("refuses %j", (text, message) => {
    expect(() => parsePorts(text)).toThrow(message);
  });

  it("allows 22/udp, which the API allows", () => {
    expect(parsePorts("22/udp")[0]?.protocol).toBe(Protocol.UDP);
  });
});

describe("formatMoney", () => {
  const money = (amountMinor: number, currency: string) => create(MoneySchema, { amountMinor: BigInt(amountMinor), currency });

  it("turns minor units into a currency amount", () => {
    expect(formatMoney(money(15, "EUR"), "en-US")).toBe("€0.15");
    expect(formatMoney(money(1234567, "USD"), "en-US")).toBe("$12,345.67");
    expect(formatMoney(money(0, "EUR"), "en-US")).toBe("€0.00");
  });

  it("knows currencies without minor units", () => {
    expect(formatMoney(money(500, "JPY"), "en-US")).toBe("¥500");
  });

  it("does not fail on an unknown currency", () => {
    expect(formatMoney(money(250, "ZZZ"), "en-US")).toContain("2.50");
  });

  it("shows a dash for nothing", () => {
    expect(formatMoney(undefined)).toBe("—");
  });
});

describe("formatting", () => {
  it("sizes", () => {
    expect(formatMiB(512n)).toBe("512 MiB");
    expect(formatMiB(2048n)).toBe("2 GiB");
    expect(formatMiB(1536)).toBe("1.5 GiB");
  });

  it("accelerators", () => {
    expect(formatAccelerator(undefined)).toBe("none");
    expect(formatAccelerator(create(ShapeSchema, { acceleratorCount: 0 }))).toBe("none");
    expect(
      formatAccelerator(create(ShapeSchema, { acceleratorKind: "nvidia", acceleratorModel: "GeForce GTX 1060 6GB", acceleratorCount: 2 })),
    ).toBe("GeForce GTX 1060 6GB × 2");
    expect(formatAccelerator(create(ShapeSchema, { acceleratorKind: "mock", acceleratorCount: 1 }))).toBe("mock × 1");
  });

  it("labels", () => {
    expect(phaseLabel(InstancePhase.UNREACHABLE)).toBe("unreachable");
    expect(capacityLabel(CapacityClass.DEDICATED)).toBe("dedicated");
    expect(protocolLabel(Protocol.UDP)).toBe("udp");
    expect(formatMinutes(125n)).toBe("125 min");
    expect(formatMinutes(undefined)).toBe("—");
    expect(shortId("9f1c2d3e-aaaa-bbbb-cccc-000000000000")).toBe("9f1c2d3e");
  });

  it("dates in the viewer's zone", () => {
    const ts = timestampFromDate(new Date(2026, 10, 8, 14, 5, 9));
    expect(formatTimestamp(ts, "en-US")).toBe("Nov 8, 2026, 2:05:09 PM");
    expect(formatTimestamp(undefined)).toBe("—");
  });

  it("knows which phases are in flight and which can be terminated", () => {
    expect(isTransitional(InstancePhase.PROVISIONING)).toBe(true);
    expect(isTransitional(InstancePhase.TERMINATING)).toBe(true);
    expect(isTransitional(InstancePhase.RUNNING)).toBe(false);
    expect(isTransitional(InstancePhase.UNREACHABLE)).toBe(false);

    expect(canTerminate(InstancePhase.PENDING)).toBe(true);
    expect(canTerminate(InstancePhase.RUNNING)).toBe(true);
    expect(canTerminate(InstancePhase.UNREACHABLE)).toBe(true);
    expect(canTerminate(InstancePhase.TERMINATING)).toBe(false);
    expect(canTerminate(InstancePhase.TERMINATED)).toBe(false);
    expect(canTerminate(InstancePhase.FAILED)).toBe(false);
    expect(canTerminate(InstancePhase.LOST)).toBe(false);
  });
});

describe("newId", () => {
  const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

  it("is a version 4 UUID", () => {
    expect(newId()).toMatch(uuid);
  });

  it("still works where crypto.randomUUID does not exist (plain HTTP)", () => {
    vi.stubGlobal("crypto", { getRandomValues: globalThis.crypto.getRandomValues.bind(globalThis.crypto) });
    const a = newId();
    expect(a).toMatch(uuid);
    expect(newId()).not.toBe(a);
  });
});

describe("useIdempotencyKey", () => {
  it("reuses the key for the same request and changes it for another", () => {
    const { result, rerender } = renderHook(() => useIdempotencyKey());
    const a = result.current("request A");
    rerender();
    expect(result.current("request A")).toBe(a);
    const b = result.current("request B");
    expect(b).not.toBe(a);
    // Going back to A is a new request as far as the server's memory goes.
    expect(result.current("request A")).not.toBe(a);
  });

  it("starts with a new key in each new form", () => {
    const first = renderHook(() => useIdempotencyKey()).result.current("same");
    const second = renderHook(() => useIdempotencyKey()).result.current("same");
    expect(first).not.toBe(second);
  });
});

describe("copyText", () => {
  function secureClipboard(writeText: (t: string) => Promise<void>) {
    vi.stubGlobal("isSecureContext", true);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
  }

  it("uses the clipboard API where it exists", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    secureClipboard(writeText);
    await expect(copyText("ssh -p 40001 ubuntu@100.64.0.5")).resolves.toBe(true);
    expect(writeText).toHaveBeenCalledWith("ssh -p 40001 ubuntu@100.64.0.5");
  });

  it("falls back to selecting text on a page that is not a secure context", async () => {
    vi.stubGlobal("isSecureContext", false);
    let copied = "";
    document.execCommand = vi.fn(() => {
      copied = (document.activeElement as HTMLTextAreaElement | null)?.value ?? document.querySelector("textarea")?.value ?? "";
      return true;
    });
    await expect(copyText("hello")).resolves.toBe(true);
    expect(copied).toBe("hello");
    // The helper textarea does not stay on the page.
    expect(document.querySelector("textarea")).toBeNull();
  });

  it("falls back when the clipboard API refuses", async () => {
    secureClipboard(vi.fn().mockRejectedValue(new Error("denied")));
    document.execCommand = vi.fn(() => true);
    await expect(copyText("x")).resolves.toBe(true);
    expect(document.execCommand).toHaveBeenCalledWith("copy");
  });

  it("says so when nothing works", async () => {
    vi.stubGlobal("isSecureContext", false);
    document.execCommand = vi.fn(() => false);
    await expect(copyText("x")).resolves.toBe(false);
  });
});
