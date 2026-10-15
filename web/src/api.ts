export type APIError = Error & { code?: string };

export async function request<T>(
  path: string,
  options: RequestInit = {},
  csrfToken?: string,
): Promise<T> {
  const headers = new Headers(options.headers);
  if (csrfToken) headers.set("X-CSRF-Token", csrfToken);
  if (options.body) headers.set("Content-Type", "application/json");
  const response = await fetch(path, {
    ...options,
    headers,
    credentials: "same-origin",
  });
  const payload = await response.json();
  if (!response.ok) {
    const error = new Error(payload.message ?? "The request failed.") as APIError;
    error.code = payload.error;
    throw error;
  }
  return payload as T;
}
