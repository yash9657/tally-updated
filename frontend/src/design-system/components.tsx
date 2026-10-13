import type { AnchorHTMLAttributes, ButtonHTMLAttributes, ReactNode } from "react";

type ButtonVariant = "primary" | "secondary" | "quiet";
const buttonClass = (variant: ButtonVariant, className = "") =>
  `tw-button tw-button--${variant} ${className}`.trim();

export function Button({ variant = "primary", className, type = "button", ...props }:
  ButtonHTMLAttributes<HTMLButtonElement> & { variant?: ButtonVariant }) {
  return <button type={type} className={buttonClass(variant, className)} {...props} />;
}

export function ButtonLink({ variant = "primary", className, ...props }:
  AnchorHTMLAttributes<HTMLAnchorElement> & { variant?: ButtonVariant }) {
  return <a className={buttonClass(variant, className)} {...props} />;
}

export function Brand({ href = "/", compact = false }: { href?: string; compact?: boolean }) {
  return <a className="tw-brand" href={href} aria-label="Tallyweb home">
    <img src="/brand/tallyweb-mark.svg" width="36" height="36" alt="" />
    {!compact && <span>tally<span className="tw-brand__suffix">web</span><span className="tw-brand__dot">.</span></span>}
  </a>;
}

export function ArrowIcon() {
  return <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M4 12h15m-6-6 6 6-6 6" /></svg>;
}

export function Keycap({ children }: { children: ReactNode }) {
  return <kbd className="tw-keycap">{children}</kbd>;
}
