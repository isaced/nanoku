const STORAGE_KEY = 'nanoku:creds';

export type Credentials = { user: string; pass: string };

export function getCredentials(): Credentials | null {
  try {
    const raw = sessionStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Credentials;
    if (!parsed.user || !parsed.pass) return null;
    return parsed;
  } catch {
    return null;
  }
}

export function setCredentials(creds: Credentials) {
  sessionStorage.setItem(STORAGE_KEY, JSON.stringify(creds));
}

export function clearCredentials() {
  sessionStorage.removeItem(STORAGE_KEY);
}