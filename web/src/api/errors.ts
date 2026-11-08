import { Code, ConnectError } from "@connectrpc/connect";

/** Normalizes anything thrown by a call into a ConnectError. */
export function toConnectError(e: unknown): ConnectError {
  return ConnectError.from(e);
}

/** The text to show a person: the server's message without the "[code]" prefix. */
export function errorMessage(e: unknown): string {
  const err = toConnectError(e);
  return err.rawMessage || Code[err.code] || "request failed";
}

/** The session is missing, expired or logged out. */
export function isUnauthenticated(e: unknown): boolean {
  return toConnectError(e).code === Code.Unauthenticated;
}

/**
 * Errors that repeating the request cannot fix. A watch that fails like this
 * stays failed instead of reconnecting.
 */
export function isPermanent(e: unknown): boolean {
  switch (toConnectError(e).code) {
    case Code.Unauthenticated:
    case Code.PermissionDenied:
    case Code.NotFound:
    case Code.InvalidArgument:
    case Code.Unimplemented:
      return true;
    default:
      return false;
  }
}
