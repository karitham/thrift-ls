import { createHash } from 'crypto';

/**
 * Pure decision logic for locating the thrift-ls binary. No I/O: filesystem
 * existence and environment are injected by the caller, so every function is
 * unit-testable without mocks.
 */

// Release asset names are `thrift-ls-<os>-<arch>[.exe]`, matching the names
// the release workflow attaches. `process.platform`/`process.arch` map onto
// them; anything else has no prebuilt binary.
const ASSET_OS: Readonly<Record<string, string>> = {
  linux: 'linux',
  darwin: 'darwin',
  win32: 'windows',
};
const GO_ARCH: Readonly<Record<string, string>> = {
  x64: 'amd64',
  arm64: 'arm64',
};

/**
 * assetName returns the release asset file name for the running platform,
 * or undefined when no prebuilt binary exists (no throw: an unsupported
 * platform is a value, not an exceptional condition).
 */
export function assetName(platform: string, arch: string): string | undefined {
  const os = ASSET_OS[platform];
  const goarch = GO_ARCH[arch];
  if (!os || !goarch) {
    return undefined;
  }
  return `thrift-ls-${os}-${goarch}${platform === 'win32' ? '.exe' : ''}`;
}

/**
 * findOnPath searches PATH (in order) for a `thrift-ls` executable. On
 * Windows each PATHEXT extension is tried per directory. sep/delimiter are
 * injected so the search is testable on any host.
 */
export function findOnPath(opts: {
  platform: string;
  pathEnv?: string;
  pathext?: string;
  sep: string;
  delimiter: string;
  exists: (candidate: string) => boolean;
}): string | undefined {
  const { platform, pathEnv, pathext, sep, delimiter, exists } = opts;
  const exts = platform === 'win32'
    ? (pathext ?? '.COM;.EXE;.BAT;.CMD').split(';').filter(Boolean)
    : [''];
  for (const dir of (pathEnv ?? '').split(delimiter).filter(Boolean)) {
    for (const ext of exts) {
      const candidate = `${dir}${sep}thrift-ls${ext.toLowerCase()}`;
      if (exists(candidate)) {
        return candidate;
      }
    }
  }
  return undefined;
}

/**
 * parseChecksums parses `sha256sum` output (`<hash>  <name>` per line) into
 * a filename → hash map.
 */
export function parseChecksums(text: string): ReadonlyMap<string, string> {
  const checksums = new Map<string, string>();
  for (const line of text.split('\n')) {
    const parts = line.trim().split(/\s+/);
    if (parts.length >= 2) {
      checksums.set(parts[1], parts[0]);
    }
  }
  return checksums;
}

/** sha256Hex returns the lowercase hex sha256 of bytes. */
export function sha256Hex(bytes: Buffer): string {
  return createHash('sha256').update(bytes).digest('hex');
}
