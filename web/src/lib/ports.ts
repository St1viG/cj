import { create } from "@bufbuild/protobuf";
import { type PortSpec, PortSpecSchema, Protocol } from "@gen/kondukter/api/v1/types_pb";

/** The limit the API enforces (`MaxPorts`). */
export const MAX_PORTS = 16;

/**
 * Reads the ports a customer typed: `8080`, `8080/tcp`, `53/udp`, separated by
 * commas, spaces or new lines. A bare number is TCP. Throws an Error whose
 * message says what is wrong, for the form to show.
 *
 * 22/tcp is refused, as the API does: it is always exposed.
 */
export function parsePorts(text: string): PortSpec[] {
  const out: PortSpec[] = [];
  const seen = new Set<string>();
  for (const token of text.split(/[\s,]+/).filter(Boolean)) {
    const m = /^(\d{1,5})(?:\/(tcp|udp))?$/i.exec(token);
    if (!m) throw new Error(`"${token}" is not a port: write 8080, 8080/tcp or 53/udp.`);
    const port = Number(m[1]);
    const proto = (m[2] ?? "tcp").toLowerCase();
    if (port < 1 || port > 65535) throw new Error(`${token}: the port must be between 1 and 65535.`);
    if (port === 22 && proto === "tcp") throw new Error("22/tcp is always exposed; leave it out.");
    const id = `${port}/${proto}`;
    if (seen.has(id)) throw new Error(`${id} is listed twice.`);
    seen.add(id);
    out.push(create(PortSpecSchema, { guestPort: port, protocol: proto === "udp" ? Protocol.UDP : Protocol.TCP }));
  }
  if (out.length > MAX_PORTS) throw new Error(`At most ${MAX_PORTS} ports.`);
  return out;
}
