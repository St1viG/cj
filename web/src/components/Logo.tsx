/** The Warden wordmark: a registration-marked square with a signal at its centre. */
export function Logo({ size = 28 }: { size?: number }) {
  return (
    <span className="wm" style={{ fontSize: size }}>
      <svg width={size} height={size} viewBox="0 0 26 26" fill="none" aria-hidden="true">
        <rect x="1.5" y="1.5" width="23" height="23" stroke="var(--ink-h)" strokeWidth="1.2" />
        <path d="M1.5 13H9M17 13h7.5" stroke="var(--ink-h)" strokeWidth="1.2" />
        <circle cx="13" cy="13" r="3.2" fill="var(--acc)" />
      </svg>
      Warden
    </span>
  );
}
