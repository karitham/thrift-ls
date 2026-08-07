import { spawnSync } from 'child_process';
import * as crypto from 'crypto';
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

const REPO = 'karitham/thrift-ls';
const RELEASES_URL = `https://github.com/${REPO}/releases`;
const DOWNLOAD_URL = `${RELEASES_URL}/latest/download`;

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

  const onPath = findOnPath();
  if (onPath) {
    return onPath;
  }

  const cached = cachedBinaryPath(context);
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

  return downloadBinary(context);
}

function findOnPath(): string | undefined {
  const exts = process.platform === 'win32'
    ? (process.env.PATHEXT ?? '.COM;.EXE;.BAT;.CMD').split(';').filter(Boolean)
    : [''];
  const dirs = (process.env.PATH ?? '').split(path.delimiter).filter(Boolean);
  for (const dir of dirs) {
    for (const ext of exts) {
      const candidate = path.join(dir, `thrift-ls${ext.toLowerCase()}`);
      if (fs.existsSync(candidate)) {
        return candidate;
      }
    }
  }
  return undefined;
}

async function reinstall(context: ExtensionContext): Promise<void> {
  const configured = workspace.getConfiguration('thrift-ls').get<string>('path');
  if (configured) {
    window.showInformationMessage(
      'thrift-ls.path is set, so it takes precedence over the downloaded binary.'
    );
    return;
  }
  const bin = await downloadBinary(context, true);
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

async function downloadBinary(
  context: ExtensionContext,
  force = false
): Promise<string | undefined> {
  const dest = cachedBinaryPath(context);
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
      let name: string;
      try {
        name = binaryName();
      } catch (err) {
        window.showErrorMessage(
          `Cannot download thrift-ls: ${err instanceof Error ? err.message : String(err)}. See ${RELEASES_URL}`
        );
        return undefined;
      }

      const url = `${DOWNLOAD_URL}/${name}`;

      try {
        const expected = await fetchChecksum(`${DOWNLOAD_URL}/checksums.txt`, name);
        const bytes = await fetchBytes(url);
        if (expected) {
          const actual = crypto.createHash('sha256').update(bytes).digest('hex');
          if (actual !== expected) {
            throw new Error(
              `checksum mismatch for ${name} (got ${actual.slice(0, 12)}…, want ${expected.slice(0, 12)}…)`
            );
          }
        }

        await fs.promises.mkdir(path.dirname(dest), { recursive: true });
        const tmp = `${dest}.tmp-${process.pid}`;
        await fs.promises.writeFile(tmp, bytes);
        makeExecutable(tmp);
        try {
          await fs.promises.rename(tmp, dest);
        } catch (err) {
          await fs.promises.rm(tmp, { force: true }).catch(() => undefined);
          if (process.platform === 'win32') {
            throw new Error(
              `could not replace ${name}: the previous copy may still be in use by a running server. Reload the window and retry (${err instanceof Error ? err.message : String(err)})`
            );
          }
          throw err;
        }

        const version = versionOf(dest);
        window.showInformationMessage(
          `thrift-ls${version ? ` ${version}` : ''} installed to ${dest}`
        );
        return dest;
      } catch (err) {
        window.showErrorMessage(
          `Failed to download thrift-ls: ${err instanceof Error ? err.message : String(err)}. See ${RELEASES_URL}`
        );
        return undefined;
      }
    }
  );
}

async function fetchChecksum(checksumUrl: string, name: string): Promise<string | undefined> {
  const res = await fetch(checksumUrl);
  if (res.status === 404) {
    return undefined; // release predates checksums.txt; proceed unverified
  }
  if (!res.ok) {
    throw new Error(`HTTP ${res.status} for ${checksumUrl}`);
  }
  const text = await res.text();
  for (const line of text.split('\n')) {
    const parts = line.trim().split(/\s+/);
    if (parts[1] === name) {
      return parts[0];
    }
  }
  return undefined;
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

function cachedBinaryPath(context: ExtensionContext): string {
  return path.join(context.globalStorageUri.fsPath, 'bin', binaryName());
}

function binaryName(): string {
  return `thrift-ls-${osName()}-${archName()}${process.platform === 'win32' ? '.exe' : ''}`;
}

function osName(): string {
  switch (process.platform) {
    case 'win32':
      return 'windows';
    case 'darwin':
      return 'darwin';
    case 'linux':
      return 'linux';
    default:
      throw new Error(`unsupported platform: ${process.platform}`);
  }
}

function archName(): string {
  switch (process.arch) {
    case 'x64':
      return 'amd64';
    case 'arm64':
      return 'arm64';
    default:
      throw new Error(`unsupported architecture: ${process.arch}`);
  }
}

function makeExecutable(file: string): void {
  if (process.platform !== 'win32') {
    fs.chmodSync(file, 0o755);
  }
}

export { deactivate };
