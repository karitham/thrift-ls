import { spawnSync } from 'child_process';
import * as fs from 'fs';
import * as path from 'path';
import {
  commands,
  env,
  ExtensionContext,
  ProgressLocation,
  Uri,
  window,
  workspace,
} from 'vscode';
import {
  LanguageClient,
  LanguageClientOptions,
  ServerOptions,
} from 'vscode-languageclient/node';
import { assetName, findOnPath, parseChecksums, releaseTag, sha256Hex } from './platform';

const REPO = 'karitham/thrift-ls';
const RELEASES_URL = `https://github.com/${REPO}/releases`;

// The constructs with per-construct separator and break settings.
const CONSTRUCTS = [
  'structs',
  'unions',
  'exceptions',
  'enums',
  'arguments',
  'throws',
  'lists',
  'maps',
  'sets',
] as const;

// The release asset and its checksum file for the running platform, or
// undefined when no prebuilt binary exists for it.
interface ReleaseTarget {
  name: string;
  url: string;
  checksumUrl: string;
}

let client: LanguageClient | undefined;

export async function activate(context: ExtensionContext) {
  context.subscriptions.push(
    commands.registerCommand('thrift-ls.downloadServer', () => reinstall(context)),
    commands.registerCommand('thrift-ls.openReleases', () =>
      env.openExternal(Uri.parse(RELEASES_URL))
    ),
  );

  const bin = await resolveBinary(context);
  if (!bin) {
    return;
  }

  const serverOptions: ServerOptions = { command: bin, args: ['lsp'] };
  const clientOptions: LanguageClientOptions = {
    documentSelector: [{ language: 'thrift' }],
    // Formatter settings are sent as initializationOptions; the client
    // re-sends them via didChangeConfiguration when settings change, so the
    // server re-formats with the new values without a restart.
    initializationOptions: formatSettings(),
    synchronize: { configurationSection: 'thrift-ls' },
  };

  client = new LanguageClient(
    'thrift-ls',
    'Thrift Language Server',
    serverOptions,
    clientOptions
  );
  void client.start();
  context.subscriptions.push({ dispose: () => void client?.stop() });
}

function deactivate(): Thenable<void> | undefined {
  return client?.stop();
}

/**
 * resolveBinary returns the path of a usable thrift-ls binary: the
 * configured path, then PATH, then a cached download, then a prompt that
 * offers to download the release binary. Undefined means no server this
 * session.
 */
/**
 * formatSettings returns the thrift-ls formatter settings as the options
 * document the server expects (the thrift-ls.json schema). The `path`
 * setting is not a server option and is left out; the server drops it
 * defensively anyway.
 */
function formatSettings(): Record<string, unknown> {
  const cfg = workspace.getConfiguration('thrift-ls');
  const opts: Record<string, unknown> = {};
  for (const key of ['printWidth', 'indent', 'tabWidth', 'align'] as const) {
    const value = cfg.get(key);
    if (value !== undefined) {
      opts[key] = value;
    }
  }

  const separators: Record<string, string> = {};
  const breaks: Record<string, boolean> = {};
  for (const construct of CONSTRUCTS) {
    const separator = cfg.get<string>(`separators.${construct}`);
    if (separator !== undefined) {
      separators[construct] = separator;
    }
    const brk = cfg.get<boolean>(`break.${construct}`);
    if (brk !== undefined) {
      breaks[construct] = brk;
    }
  }
  if (Object.keys(separators).length > 0) {
    opts.separators = separators;
  }
  if (Object.keys(breaks).length > 0) {
    opts.break = breaks;
  }

  return opts;
}

async function resolveBinary(context: ExtensionContext): Promise<string | undefined> {
  const configured = workspace.getConfiguration('thrift-ls').get<string>('path');
  if (configured) {
    if (!fs.existsSync(configured)) {
      window.showErrorMessage(
        `thrift-ls.path points at "${configured}", which does not exist. Fix the setting or reload.`
      );
      return undefined;
    }
    return configured;
  }

  const onPath = findOnPath({
    platform: process.platform,
    pathEnv: process.env.PATH,
    pathext: process.env.PATHEXT,
    sep: path.sep,
    delimiter: path.delimiter,
    exists: fs.existsSync,
  });
  if (onPath) {
    return onPath;
  }

  const target = releaseTarget(context);
  if (!target) {
    window.showErrorMessage(
      `thrift-ls publishes no binary for ${process.platform}/${process.arch}. Install from source or set thrift-ls.path.`
    );
    return undefined;
  }

  const cached = cachedBinaryPath(context, target);
  if (fs.existsSync(cached)) {
    makeExecutable(cached);
    return cached;
  }

  const choice = await window.showInformationMessage(
    'The thrift-ls language server binary was not found on PATH. Download the latest release?',
    'Download',
    'Open Releases Page'
  );
  if (choice === 'Open Releases Page') {
    await env.openExternal(Uri.parse(RELEASES_URL));
    window.showInformationMessage('Install thrift-ls, then reload the window.');
    return undefined;
  }
  if (choice !== 'Download') {
    return undefined;
  }

  return downloadBinary(context, target);
}

async function reinstall(context: ExtensionContext): Promise<void> {
  const configured = workspace.getConfiguration('thrift-ls').get<string>('path');
  if (configured) {
    window.showInformationMessage(
      'thrift-ls.path is set, so it takes precedence over the downloaded binary.'
    );
    return;
  }

  const target = releaseTarget(context);
  if (!target) {
    window.showErrorMessage(
      `thrift-ls publishes no binary for ${process.platform}/${process.arch}.`
    );
    return;
  }

  const bin = await downloadBinary(context, target, true);
  if (!bin) {
    return;
  }
  const choice = await window.showInformationMessage(
    'thrift-ls downloaded. Reload the window to restart the server with it.',
    'Reload Window'
  );
  if (choice === 'Reload Window') {
    await commands.executeCommand('workbench.action.reloadWindow');
  }
}

/**
 * downloadBinary fetches, verifies, and installs the release binary,
 * replacing a previously downloaded copy. Returns the installed path, or
 * undefined on failure (an error message is shown).
 */
async function downloadBinary(
  context: ExtensionContext,
  target: ReleaseTarget,
  force = false
): Promise<string | undefined> {
  const dest = cachedBinaryPath(context, target);
  if (!force && fs.existsSync(dest)) {
    return dest;
  }

  return window.withProgress(
    {
      location: ProgressLocation.Notification,
      title: 'Downloading thrift-ls…',
      cancellable: false,
    },
    async () => {
      try {
        // Gather (impure): fetch the checksum and the binary.
        const bytes = await fetchVerified(target);
        // Commit (impure): write and atomically replace.
        await installBinary(bytes, target.name, dest);
      } catch (err) {
        window.showErrorMessage(
          `Failed to download thrift-ls: ${errMessage(err)}. See ${RELEASES_URL}`
        );
        return undefined;
      }

      const version = versionOf(dest);
      window.showInformationMessage(
        `thrift-ls${version ? ` ${version}` : ''} installed to ${dest}`
      );
      return dest;
    }
  );
}

/** fetchVerified downloads the binary and aborts on checksum mismatch. */
async function fetchVerified(target: ReleaseTarget): Promise<Buffer> {
  const expected = await fetchChecksum(target.checksumUrl, target.name);
  const bytes = await fetchBytes(target.url);
  if (expected && sha256Hex(bytes) !== expected) {
    throw new Error(
      `checksum mismatch for ${target.name} (want ${expected.slice(0, 12)}…)`
    );
  }
  return bytes;
}

/** installBinary writes the binary to dest, replacing any previous copy. */
async function installBinary(bytes: Buffer, name: string, dest: string): Promise<void> {
  await fs.promises.mkdir(path.dirname(dest), { recursive: true });
  const tmp = `${dest}.tmp-${process.pid}`;
  await fs.promises.writeFile(tmp, bytes);
  makeExecutable(tmp);
  try {
    await fs.promises.rename(tmp, dest);
  } catch (err) {
    await fs.promises.rm(tmp, { force: true }).catch(() => undefined);
    if (process.platform === 'win32') {
      // Windows cannot replace a file that is in use; the previous copy is
      // the running server process.
      throw new Error(
        `could not replace ${name}: the previous copy may still be in use by a running server. Reload the window and retry (${errMessage(err)})`
      );
    }
    throw err;
  }
}

async function fetchChecksum(checksumUrl: string, name: string): Promise<string | undefined> {
  const res = await fetch(checksumUrl);
  if (res.status === 404) {
    return undefined; // release predates checksums.txt; proceed unverified
  }
  if (!res.ok) {
    throw new Error(`HTTP ${res.status} for ${checksumUrl}`);
  }
  return parseChecksums(await res.text()).get(name);
}

async function fetchBytes(url: string): Promise<Buffer> {
  const res = await fetch(url);
  if (!res.ok) {
    throw new Error(`HTTP ${res.status} for ${url}`);
  }
  return Buffer.from(await res.arrayBuffer());
}

function versionOf(bin: string): string | undefined {
  const out = spawnSync(bin, ['--version'], { encoding: 'utf8', timeout: 10_000 });
  const text = out.stdout?.trim() || out.stderr?.trim();
  return text || undefined;
}

function releaseTarget(context: ExtensionContext): ReleaseTarget | undefined {
  const name = assetName(process.platform, process.arch);
  if (!name) {
    return undefined;
  }

  // A dev vsix pins itself to its own commit's prerelease, so it pairs
  // with the binaries built from the same code; stable builds use
  // releases/latest (which never resolves to a prerelease).
  const tag = releaseTag(context.extension.packageJSON.version);
  const base = tag ? `${RELEASES_URL}/download/${tag}` : `${RELEASES_URL}/latest/download`;

  return {
    name,
    url: `${base}/${name}`,
    checksumUrl: `${base}/checksums.txt`,
  };
}

function cachedBinaryPath(context: ExtensionContext, target: ReleaseTarget): string {
  return path.join(context.globalStorageUri.fsPath, 'bin', target.name);
}

function makeExecutable(file: string): void {
  if (process.platform !== 'win32') {
    fs.chmodSync(file, 0o755);
  }
}

function errMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

export { deactivate };
