import { describe, expect, it } from "vitest";
import { generateSshKeyPair, sshKeyPairFromSeed } from "./sshKey";

// Made from the seed 0, 1, 2 … 31 and check value 0x01020304, then fed to
// `ssh-keygen -y -f`, which accepted the private key and printed the same
// public key. The test keeps the format from drifting.
const GOLDEN_PUBLIC = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAOhB7/zzhC+HXDdGOdLwJln5NYwm6UNXx3chmQSVTG4 golden";
const GOLDEN_PRIVATE =
  "-----BEGIN OPENSSH PRIVATE KEY-----\n" +
  "b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW\n" +
  "QyNTUxOQAAACADoQe/884Qvh1w3RjnS8CZZ+TWMJulDV8d3IZkElUxuAAAAJABAgMEAQID\n" +
  "BAAAAAtzc2gtZWQyNTUxOQAAACADoQe/884Qvh1w3RjnS8CZZ+TWMJulDV8d3IZkElUxuA\n" +
  "AAAEAAAQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHwOhB7/zzhC+HXDdGOdLwJln\n" +
  "5NYwm6UNXx3chmQSVTG4AAAABmdvbGRlbgECAwQFBgc=\n" +
  "-----END OPENSSH PRIVATE KEY-----\n";

describe("sshKeyPairFromSeed", () => {
  it("makes the same OpenSSH key files that ssh-keygen accepts", () => {
    const seed = Uint8Array.from({ length: 32 }, (_, i) => i);
    const k = sshKeyPairFromSeed(seed, "golden", 0x01020304);
    expect(k.publicKey).toBe(GOLDEN_PUBLIC);
    expect(k.privateKey).toBe(GOLDEN_PRIVATE);
  });

  it("pads the private section to the cipher block size and wraps at 70 columns", () => {
    for (const comment of ["", "a", "kondukter-demo", "a-much-longer-comment-for-the-key"]) {
      const k = sshKeyPairFromSeed(new Uint8Array(32).fill(7), comment, 1);
      const lines = k.privateKey.trimEnd().split("\n");
      expect(lines[0]).toBe("-----BEGIN OPENSSH PRIVATE KEY-----");
      expect(lines[lines.length - 1]).toBe("-----END OPENSSH PRIVATE KEY-----");
      expect(lines.slice(1, -1).every((l) => l.length <= 70)).toBe(true);
      const raw = Uint8Array.from(atob(lines.slice(1, -1).join("")), (c) => c.charCodeAt(0));
      // magic(15) + 3 strings + count + public blob + private section
      const view = new DataView(raw.buffer);
      let at = 15;
      for (let i = 0; i < 3; i++) at += 4 + view.getUint32(at); // "none", "none", ""
      at += 4; // number of keys
      at += 4 + view.getUint32(at); // public blob
      const privLen = view.getUint32(at);
      expect(at + 4 + privLen).toBe(raw.length);
      expect(privLen % 8).toBe(0);
    }
  });

  it("rejects a seed of the wrong size and a multi-line comment", () => {
    expect(() => sshKeyPairFromSeed(new Uint8Array(31), "x", 1)).toThrow(/32 bytes/);
    expect(() => sshKeyPairFromSeed(new Uint8Array(32), "a\nb", 1)).toThrow(/one line/);
  });
});

describe("generateSshKeyPair", () => {
  it("makes a different key each time, with the comment on the public line", () => {
    const a = generateSshKeyPair("demo");
    const b = generateSshKeyPair("demo");
    expect(a.publicKey).toMatch(/^ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA\S+ demo$/);
    expect(a.publicKey).not.toBe(b.publicKey);
    expect(a.privateKey).not.toBe(b.privateKey);
  });
});
