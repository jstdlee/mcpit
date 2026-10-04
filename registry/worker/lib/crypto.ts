// Ed25519 for device keys (verify) and the registry key (sign), via WebCrypto.

export const b64 = {
  enc: (b: ArrayBuffer | Uint8Array) => btoa(String.fromCharCode(...new Uint8Array(b))),
  dec: (s: string) => Uint8Array.from(atob(s), (c) => c.charCodeAt(0)),
};

export async function sha256hexBytes(data: Uint8Array): Promise<string> {
  const d = await crypto.subtle.digest('SHA-256', data as BufferSource);
  return [...new Uint8Array(d)].map((x) => x.toString(16).padStart(2, '0')).join('');
}

/** keyId matches the Go client: "key:ed25519:" + first 16 hex chars of sha256(public key). */
export async function keyId(publicKeyB64: string): Promise<string> {
  return 'key:ed25519:' + (await sha256hexBytes(b64.dec(publicKeyB64))).slice(0, 16);
}

export async function verifyEd25519(publicKeyB64: string, message: string, signatureB64: string): Promise<boolean> {
  try {
    const raw = b64.dec(publicKeyB64);
    if (raw.length !== 32) return false;
    const key = await crypto.subtle.importKey('raw', raw, { name: 'Ed25519' }, false, ['verify']);
    return await crypto.subtle.verify(
      { name: 'Ed25519' },
      key,
      b64.dec(signatureB64),
      new TextEncoder().encode(message),
    );
  } catch {
    return false;
  }
}

/** signingString matches the Go client: METHOD\nPATH\nTS\nsha256hex(body). */
export async function signingString(method: string, path: string, ts: string, body: Uint8Array): Promise<string> {
  return `${method}\n${path}\n${ts}\n${await sha256hexBytes(body)}`;
}

export interface RegistryKey {
  id: string;
  publicB64: string;
  sign(msg: string): Promise<string>;
}

export async function loadRegistryKey(pkcs8B64: string, id: string): Promise<RegistryKey> {
  const priv = await crypto.subtle.importKey('pkcs8', b64.dec(pkcs8B64), { name: 'Ed25519' }, true, ['sign']);
  const jwk = (await crypto.subtle.exportKey('jwk', priv)) as JsonWebKey;
  const pub = Uint8Array.from(
    atob(jwk.x!.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - (jwk.x!.length % 4)) % 4)),
    (c) => c.charCodeAt(0),
  );
  return {
    id,
    publicB64: b64.enc(pub),
    sign: async (msg) => b64.enc(await crypto.subtle.sign({ name: 'Ed25519' }, priv, new TextEncoder().encode(msg))),
  };
}

export async function generateRegistryKey(): Promise<{ pkcs8: string; publicB64: string }> {
  const kp = (await crypto.subtle.generateKey({ name: 'Ed25519' }, true, ['sign', 'verify'])) as CryptoKeyPair;
  const pkcs8 = b64.enc((await crypto.subtle.exportKey('pkcs8', kp.privateKey)) as ArrayBuffer);
  const pub = b64.enc((await crypto.subtle.exportKey('raw', kp.publicKey)) as ArrayBuffer);
  return { pkcs8, publicB64: pub };
}

export function timingSafeEqual(a: string, b: string): boolean {
  const ea = new TextEncoder().encode(a);
  const eb = new TextEncoder().encode(b);
  if (ea.length !== eb.length) return false;
  let r = 0;
  for (let i = 0; i < ea.length; i++) r |= ea[i] ^ eb[i];
  return r === 0;
}
