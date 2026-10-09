// Admin-token generation for the first-boot wizard. 24 random bytes →
// 32 URL-safe base64 characters, comfortably above the appliance's 16-char
// minimum and free of whitespace (POST /auth/token rejects both).

const TOKEN_BYTES = 24

export function generateToken(): string {
  const bytes = new Uint8Array(TOKEN_BYTES)
  crypto.getRandomValues(bytes)
  let raw = ''
  for (const b of bytes) raw += String.fromCharCode(b)
  return btoa(raw).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}
