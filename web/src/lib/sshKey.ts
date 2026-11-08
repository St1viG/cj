import nacl from "tweetnacl";

/**
 * An Ed25519 SSH key pair made in the browser. The page is served over plain
 * HTTP on the tailnet, where `crypto.subtle` does not exist, so the key comes
 * from tweetnacl (pure JS, needs only `getRandomValues`). The private key never
 * leaves the page: the API gets the public line, the person gets the file.
 */
export interface SshKeyPair {
  /** One `authorized_keys` line: `ssh-ed25519 AAAA… comment`. */
  publicKey: string;
  /** An unencrypted OpenSSH private key file (`-----BEGIN OPENSSH PRIVATE KEY-----`). */
  privateKey: string;
}

const KEY_TYPE = "ssh-ed25519";
const MAGIC = "openssh-key-v1\0";

function concat(...parts: Uint8Array[]): Uint8Array {
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
  let at = 0;
  for (const p of parts) {
    out.set(p, at);
    at += p.length;
  }
  return out;
}

function u32(n: number): Uint8Array {
  return new Uint8Array([(n >>> 24) & 255, (n >>> 16) & 255, (n >>> 8) & 255, n & 255]);
}

/** SSH wire format "string": a 32-bit length, then the bytes. */
function str(b: Uint8Array | string): Uint8Array {
  const bytes = typeof b === "string" ? new TextEncoder().encode(b) : b;
  return concat(u32(bytes.length), bytes);
}

function base64(b: Uint8Array): string {
  let s = "";
  for (const x of b) s += String.fromCharCode(x);
  return btoa(s);
}

/**
 * The key pair for `seed` (32 bytes). `check` is the random 32-bit value that
 * OpenSSH repeats in the private block to detect a wrong passphrase; any value
 * works for an unencrypted key.
 */
export function sshKeyPairFromSeed(seed: Uint8Array, comment: string, check: number): SshKeyPair {
  if (seed.length !== 32) throw new Error("an Ed25519 seed is 32 bytes");
  if (/[\r\n]/.test(comment)) throw new Error("the key comment must be one line");
  const { publicKey, secretKey } = nacl.sign.keyPair.fromSeed(seed); // secretKey = seed ‖ public key
  const publicBlob = concat(str(KEY_TYPE), str(publicKey));

  // The private section, padded to the cipher block size (8 for "none") with 1, 2, 3, …
  let priv = concat(u32(check), u32(check), str(KEY_TYPE), str(publicKey), str(secretKey), str(comment));
  const pad = (8 - (priv.length % 8)) % 8;
  priv = concat(priv, Uint8Array.from({ length: pad }, (_, i) => i + 1));

  const body = concat(new TextEncoder().encode(MAGIC), str("none"), str("none"), str(""), u32(1), str(publicBlob), str(priv));
  const lines = base64(body).match(/.{1,70}/g) ?? [];
  return {
    publicKey: `${KEY_TYPE} ${base64(publicBlob)} ${comment}`,
    privateKey: ["-----BEGIN OPENSSH PRIVATE KEY-----", ...lines, "-----END OPENSSH PRIVATE KEY-----", ""].join("\n"),
  };
}

/** A new random key pair. */
export function generateSshKeyPair(comment: string): SshKeyPair {
  const random = globalThis.crypto.getRandomValues(new Uint8Array(36));
  const check = new DataView(random.buffer).getUint32(32);
  return sshKeyPairFromSeed(random.slice(0, 32), comment, check);
}
