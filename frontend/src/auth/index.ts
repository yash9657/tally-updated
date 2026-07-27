/**
 * The browser's identity layer: a password session, the WebAuthn step-up that
 * upgrades it to a token that may WRITE, the screen that obtains it, and the
 * context every consumer reads it from.
 */
export {
  AuthProvider,
  useAuth,
  AUTH_STORAGE_KEY,
  type AuthSession,
  type AuthContextValue,
  type StepUpState,
} from "./AuthContext";
export { LoginScreen } from "./LoginScreen";
export {
  stepUp,
  StepUpUnsupportedError,
  StepUpFailedError,
  type StepUpDeps,
} from "./webauthn";
export {
  login,
  setPassword,
  AuthInvalidError,
  AuthLockedError,
  type AuthUser,
  type LoginResponse,
} from "../api/auth";
