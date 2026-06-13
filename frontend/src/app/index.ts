/**
 * Public surface of the app shell. The composition root (App.tsx) and other
 * agents import from here.
 */
export { AppShell } from "./AppShell";
export { Chrome } from "./Chrome";
export {
  CompanyProvider,
  useCompanyContext,
  useClient,
  useCompanyID,
  type CompanyInfo,
} from "./CompanyContext";
export {
  ScreenRegistry,
  routeKeyFor,
  type ScreenComponent,
  type ScreenComponentProps,
  type ScreenRouteKey,
} from "./screenRegistry";
export { createDefaultRegistry } from "./defaultScreens";
