/**
 * Shared authentication helpers for kagami routes.
 */

import type { Context } from "hono";
import type { KagamiEnv } from "../types.js";

/**
 * Constant-time string comparison.
 * Uses crypto.subtle.timingSafeEqual when available (Workers runtime),
 * falls back to a constant-time XOR comparison otherwise.
 * Returns false immediately if lengths differ (length is not secret).
 */
export function timingSafeEqual(a: string, b: string): boolean {
  const encoder = new TextEncoder();
  const bufA = encoder.encode(a);
  const bufB = encoder.encode(b);
  if (bufA.byteLength !== bufB.byteLength) return false;

  // Workers runtime provides crypto.subtle.timingSafeEqual
  if (typeof crypto?.subtle?.timingSafeEqual === "function") {
    return crypto.subtle.timingSafeEqual(bufA, bufB);
  }

  // Fallback: constant-time XOR comparison
  let result = 0;
  for (let i = 0; i < bufA.byteLength; i++) {
    result |= bufA[i] ^ bufB[i];
  }
  return result === 0;
}

/**
 * Validate the project secret from the Authorization header.
 * Returns null if valid, or a 401 Response if invalid.
 */
export function validateProjectSecret(
  c: Context<{ Bindings: KagamiEnv }>,
): Response | null {
  const authHeader = c.req.header("Authorization");
  if (!authHeader) {
    return c.json(
      { error: "unauthorized", message: "Missing Authorization header" },
      401,
    );
  }

  const parts = authHeader.split(" ");
  if (parts.length !== 2 || parts[0] !== "Bearer") {
    return c.json(
      { error: "unauthorized", message: "Invalid Authorization header format" },
      401,
    );
  }

  const token = parts[1];
  if (!timingSafeEqual(token, c.env.KAGAMI_PROJECT_SECRET)) {
    return c.json(
      { error: "unauthorized", message: "Invalid project secret" },
      401,
    );
  }

  return null;
}

/**
 * Hash a secret string using SHA-256 with salt via Web Crypto API.
 * If no salt is provided, generates a random 16-byte (32 hex char) salt.
 * Returns the hex-encoded hash and the salt used.
 */
export async function hashSecret(
  secret: string,
  salt?: string,
): Promise<{ hash: string; salt: string }> {
  const actualSalt = salt ?? crypto.randomUUID().replace(/-/g, "");
  const encoder = new TextEncoder();
  const data = encoder.encode(actualSalt + secret);
  const hashBuffer = await crypto.subtle.digest("SHA-256", data);
  const hashArray = Array.from(new Uint8Array(hashBuffer));
  const hash = hashArray.map((b) => b.toString(16).padStart(2, "0")).join("");
  return { hash, salt: actualSalt };
}

/**
 * Format salt and hash into the stored "salt:hash" string.
 */
export function formatSecretHash(salt: string, hash: string): string {
  return `${salt}:${hash}`;
}

/**
 * Parse a stored "salt:hash" string back into its components.
 */
export function parseSecretHash(stored: string): { salt: string; hash: string } {
  const colonIndex = stored.indexOf(":");
  if (colonIndex === -1) {
    throw new Error("Invalid secret_hash format: missing salt separator");
  }
  const salt = stored.slice(0, colonIndex);
  const hash = stored.slice(colonIndex + 1);
  return { salt, hash };
}
