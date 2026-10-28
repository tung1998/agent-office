// Passkeys (ADR-226). Browsers allow WebAuthn only on HTTPS with a host name,
// or localhost: an office opened as http://<ip>:port keeps the password only.
// Options and answers go as JSON (parse…FromJSON / toJSON), so the server's
// base64url fields need no converting here.
interface PasskeyJSON {
  parseCreationOptionsFromJSON(o: unknown): CredentialCreationOptions['publicKey']
  parseRequestOptionsFromJSON(o: unknown): CredentialRequestOptions['publicKey']
}
type Begin = { options: { publicKey: unknown }, state: string }

// passkeyCancelled: the person closed the browser's dialog; nothing to say.
export const passkeyCancelled = (e: unknown) => e instanceof Error && (e.name === 'NotAllowedError' || e.name === 'AbortError' || e.message === 'cancelled')

const api = () => (typeof PublicKeyCredential === 'undefined' ? null : PublicKeyCredential as unknown as Partial<PasskeyJSON>)

export function usePasskey() {
  const host = typeof location === 'undefined' ? '' : location.hostname
  const supported = typeof window !== 'undefined' && window.isSecureContext
    && typeof api()?.parseRequestOptionsFromJSON === 'function'
    && !/^[\d.]+$/.test(host) && !host.includes(':') && !host.startsWith('[')

  // register adds a passkey to the signed-in account.
  async function register(name: string) {
    const b = await $fetch<Begin>('/api/me/passkeys/begin', { method: 'POST', body: {} })
    const cred = await navigator.credentials.create({ publicKey: api()!.parseCreationOptionsFromJSON!(b.options.publicKey) }) as PublicKeyCredential | null
    if (!cred) throw new Error('cancelled')
    return $fetch('/api/me/passkeys', { method: 'POST', body: { state: b.state, name, credential: cred.toJSON() } })
  }

  // login signs in with any passkey of this office, no email typed.
  async function login() {
    const b = await $fetch<Begin>('/api/auth/passkey/begin', { method: 'POST', body: {} })
    const cred = await navigator.credentials.get({ publicKey: api()!.parseRequestOptionsFromJSON!(b.options.publicKey) }) as PublicKeyCredential | null
    if (!cred) throw new Error('cancelled')
    return $fetch<{ user: OfficeUser }>('/api/auth/passkey', { method: 'POST', body: { state: b.state, credential: cred.toJSON() } })
  }

  return { supported, register, login }
}
