import { ml_kem768 } from "@noble/post-quantum/ml-kem.js";
import { ml_dsa65 } from "@noble/post-quantum/ml-dsa.js";

const API_URL = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080";

function bytesToBase64(bytes: Uint8Array): string {
  let binary = "";
  for (let i = 0; i < bytes.length; i++) {
    binary += String.fromCharCode(bytes[i]);
  }
  return btoa(binary);
}

export function base64ToBytes(b64: string): Uint8Array {
  const binary = atob(b64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes;
}

function concatBytes(...arrays: Uint8Array[]): Uint8Array {
  let totalLen = 0;
  for (const a of arrays) totalLen += a.length;
  const result = new Uint8Array(totalLen);
  let offset = 0;
  for (const a of arrays) {
    result.set(a, offset);
    offset += a.length;
  }
  return result;
}

async function deriveSessionKey(
  sharedSecret: Uint8Array,
  encapsulationKeyB64: string,
  mldsaPublicKeyB64: string
): Promise<Uint8Array> {
  const ekBytes = base64ToBytes(encapsulationKeyB64);
  const pkBytes = base64ToBytes(mldsaPublicKeyB64);
  const salt = concatBytes(ekBytes, pkBytes);

  const keyMaterial = await crypto.subtle.importKey(
    "raw",
    new Uint8Array(sharedSecret),
    "HKDF",
    false,
    ["deriveBits"]
  );

  const derivedBits = await crypto.subtle.deriveBits(
    {
      name: "HKDF",
      hash: "SHA-256",
      salt: new Uint8Array(salt),
      info: new TextEncoder().encode("qdesk-session-v1"),
    },
    keyMaterial,
    256
  );

  return new Uint8Array(derivedBits);
}

export async function performHandshake(): Promise<{
  sessionId: string;
  sessionKeyB64: string;
}> {
  // 1. Fetch server's public ML-KEM + ML-DSA key material.
  const getRes = await fetch(`${API_URL}/api/handshake`);
  if (!getRes.ok)
    throw new Error(`Failed to fetch server keys: ${getRes.status}`);
  const serverKeys: {
    encapsulation_key: string;
    mldsa_public_key: string;
  } = await getRes.json();

  const serverEkBytes = base64ToBytes(serverKeys.encapsulation_key);
  const serverPkBytes = base64ToBytes(serverKeys.mldsa_public_key);

  // 2. Generate ML-KEM-768 client keypair and encapsulate to server key.
  const clientKeys = ml_kem768.keygen();
  const { cipherText, sharedSecret } = ml_kem768.encapsulate(serverEkBytes);

  // 3. POST ciphertext to the server.
  const postRes = await fetch(`${API_URL}/api/handshake`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      ciphertext: bytesToBase64(cipherText),
      client_pub: bytesToBase64(clientKeys.publicKey),
    }),
  });
  if (!postRes.ok) {
    const body = await postRes.text();
    throw new Error(body || `Handshake POST failed: ${postRes.status}`);
  }
  const handshakeResp: {
    session_id: string;
    encapsulation_key: string;
    mldsa_public_key: string;
    signature: string;
  } = await postRes.json();

  // 4. Verify ML-DSA-65 signature BEFORE trusting the shared secret.
  //    Signed payload = UTF8(session_id) || raw(encapsulation_key) || raw(mldsa_public_key)
  //    noble verify signature: verify(signature, message, publicKey)
  const messageForVerify = concatBytes(
    new TextEncoder().encode(handshakeResp.session_id),
    base64ToBytes(handshakeResp.encapsulation_key),
    base64ToBytes(handshakeResp.mldsa_public_key)
  );
  const sigBytes = base64ToBytes(handshakeResp.signature);

  const isValid = ml_dsa65.verify(sigBytes, messageForVerify, serverPkBytes);
  if (!isValid) {
    throw new Error(
      "ML-DSA-65 signature verification failed — possible MITM attack"
    );
  }

  // 5. Derive 32-byte HKDF session key (matching server derivation exactly).
  const sessionKey = await deriveSessionKey(
    sharedSecret,
    handshakeResp.encapsulation_key,
    handshakeResp.mldsa_public_key
  );

  return {
    sessionId: handshakeResp.session_id,
    sessionKeyB64: bytesToBase64(sessionKey),
  };
}

// sha256Hex returns the lowercase hex SHA-256 digest of the UTF-8 encoding of
// the given strings concatenated in order.
export async function sha256Hex(...parts: string[]): Promise<string> {
  const data = new TextEncoder().encode(parts.join(""));
  const digest = await crypto.subtle.digest("SHA-256", data);
  const bytes = new Uint8Array(digest);
  let hex = "";
  for (const b of bytes) hex += b.toString(16).padStart(2, "0");
  return hex;
}

// verifyVersionChain recomputes SHA256(content_hash || parent_hash || badge ||
// created_at) and reports whether it matches the row's stored sha256_hash.
// The row's hash fields are exactly as returned by the backend. When content
// is withheld (empty content_hash) the chain cannot be verified, so it returns
// "unverified" rather than falsely flagging a tamper.
export async function verifyVersionChain(row: {
  sha256_hash: string;
  content_hash: string;
  parent_sha256_hash: string;
  badge_number: string;
  created_at: string;
}): Promise<"valid" | "invalid" | "unverified"> {
  if (!row.content_hash) return "unverified";
  const expected = await sha256Hex(
    row.content_hash,
    row.parent_sha256_hash,
    row.badge_number,
    row.created_at
  );
  return expected === row.sha256_hash ? "valid" : "invalid";
}
