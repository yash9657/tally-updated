/**
 * Public API surface for the `cored` client. Consumers import from "@/api"
 * (i.e. ./api) and depend only on the {@link CoredClient} interface plus the
 * request/response types — never on a concrete implementation.
 */
export type { CoredClient } from "./client";
export { createRestClient } from "./rest";
export { createMockClient } from "./mock";
export type { MockClientOptions } from "./mock";
export {
  createHttpClient,
  ApiError,
  type HttpClient,
  type HttpClientOptions,
} from "./http";
export * from "./types";
