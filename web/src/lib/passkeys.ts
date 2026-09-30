/**
 * Passkeys in the browser.
 *
 * The panel sends WebAuthn options as JSON, with every binary value — the
 * challenge, the user handle, credential ids — in base64url, and wants the
 * browser's answer back the same way. This is the translation in both
 * directions, and the two calls into navigator.credentials. It does it by hand
 * rather than with PublicKeyCredential.parseCreationOptionsFromJSON and
 * toJSON, which not every browser that supports passkeys has yet.
 */

/** What POST /api/me/passkeys/register answers. */
export type PasskeyCreationOptions = { publicKey: PublicKeyCredentialCreationOptionsJSON }

/** What POST /api/auth/passkey/begin answers. */
export type PasskeyRequestOptions = { publicKey: PublicKeyCredentialRequestOptionsJSON }

/**
 * Whether this browser can use a passkey here at all: it has WebAuthn, and the
 * page is a secure context. The panel says separately whether its own address
 * is one a passkey can belong to.
 */
export function passkeysSupported(): boolean {
  return (
    typeof window !== "undefined" &&
    window.isSecureContext &&
    typeof window.PublicKeyCredential === "function" &&
    typeof navigator.credentials?.get === "function"
  )
}

/**
 * Whether the browser can offer passkeys in a field's autofill suggestions,
 * which is how the sign-in page offers them without anybody pressing a button.
 */
export async function autofillAvailable(): Promise<boolean> {
  if (!passkeysSupported()) return false
  const check = window.PublicKeyCredential.isConditionalMediationAvailable
  if (typeof check !== "function") return false
  try {
    return await check.call(window.PublicKeyCredential)
  } catch {
    return false
  }
}

/** Makes a passkey for the options the panel gave, and answers in its JSON. */
export async function createPasskey(options: PasskeyCreationOptions): Promise<unknown> {
  const { publicKey } = options
  const credential = await navigator.credentials.create({
    publicKey: {
      ...publicKey,
      challenge: fromBase64URL(publicKey.challenge),
      user: { ...publicKey.user, id: fromBase64URL(publicKey.user.id) },
      excludeCredentials: publicKey.excludeCredentials?.map(descriptor),
      authenticatorSelection: publicKey.authenticatorSelection,
      attestation: publicKey.attestation as AttestationConveyancePreference | undefined,
      extensions: publicKey.extensions as AuthenticationExtensionsClientInputs | undefined,
    },
  })
  if (!(credential instanceof PublicKeyCredential)) {
    throw new DOMException("The browser made no passkey.", "NotAllowedError")
  }
  const response = credential.response as AuthenticatorAttestationResponse
  return {
    id: credential.id,
    rawId: toBase64URL(credential.rawId),
    type: credential.type,
    authenticatorAttachment: credential.authenticatorAttachment ?? undefined,
    clientExtensionResults: credential.getClientExtensionResults(),
    response: {
      clientDataJSON: toBase64URL(response.clientDataJSON),
      attestationObject: toBase64URL(response.attestationObject),
      transports: typeof response.getTransports === "function" ? response.getTransports() : [],
    },
  }
}

/**
 * Signs the panel's challenge with a passkey the person picks, and answers in
 * the panel's JSON. With mediation "conditional" the browser offers the
 * passkeys in the email field's suggestions instead of opening a dialog, and
 * waits until one is picked or signal aborts.
 */
export async function getPasskey(
  options: PasskeyRequestOptions,
  how: { mediation?: CredentialMediationRequirement; signal?: AbortSignal } = {},
): Promise<unknown> {
  const { publicKey } = options
  const credential = await navigator.credentials.get({
    mediation: how.mediation,
    signal: how.signal,
    publicKey: {
      ...publicKey,
      challenge: fromBase64URL(publicKey.challenge),
      allowCredentials: publicKey.allowCredentials?.map(descriptor),
      userVerification: publicKey.userVerification as UserVerificationRequirement | undefined,
      extensions: publicKey.extensions as AuthenticationExtensionsClientInputs | undefined,
    },
  })
  if (!(credential instanceof PublicKeyCredential)) {
    throw new DOMException("The browser used no passkey.", "NotAllowedError")
  }
  const response = credential.response as AuthenticatorAssertionResponse
  return {
    id: credential.id,
    rawId: toBase64URL(credential.rawId),
    type: credential.type,
    authenticatorAttachment: credential.authenticatorAttachment ?? undefined,
    clientExtensionResults: credential.getClientExtensionResults(),
    response: {
      clientDataJSON: toBase64URL(response.clientDataJSON),
      authenticatorData: toBase64URL(response.authenticatorData),
      signature: toBase64URL(response.signature),
      userHandle: response.userHandle ? toBase64URL(response.userHandle) : undefined,
    },
  }
}

/**
 * Why the browser, rather than the panel, did not go through with it, as the
 * last part of a translation key: auth.passkeys.failure.<this>. Null when the
 * failure is not the browser's, and the panel's own answer should be shown.
 */
export type PasskeyFailure = "cancelled" | "exists" | "wrongAddress" | "failed"

export function passkeyFailure(error: unknown): PasskeyFailure | null {
  if (!(error instanceof DOMException)) return null
  switch (error.name) {
    // The person closed the dialog, or let it time out. Browsers say the same
    // for both, on purpose, so a page cannot tell which passkeys exist.
    case "NotAllowedError":
    case "AbortError":
      return "cancelled"
    // A passkey on the exclude list: this authenticator already has one.
    case "InvalidStateError":
      return "exists"
    // The page is not at the hostname the passkey belongs to, such as the
    // sslip.io address of a panel whose Panel URL is its domain.
    case "SecurityError":
      return "wrongAddress"
    default:
      return "failed"
  }
}

function descriptor(credential: PublicKeyCredentialDescriptorJSON): PublicKeyCredentialDescriptor {
  return {
    type: credential.type as PublicKeyCredentialType,
    id: fromBase64URL(credential.id),
    transports: credential.transports as AuthenticatorTransport[] | undefined,
  }
}

function fromBase64URL(value: string): ArrayBuffer {
  const base64 = value.replace(/-/g, "+").replace(/_/g, "/")
  const padded = base64 + "=".repeat((4 - (base64.length % 4)) % 4)
  const binary = atob(padded)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
  return bytes.buffer
}

function toBase64URL(buffer: ArrayBuffer): string {
  const bytes = new Uint8Array(buffer)
  let binary = ""
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "")
}
