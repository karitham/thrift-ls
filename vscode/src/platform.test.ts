import assert from 'node:assert';
import { test } from 'node:test';
import { assetName, findOnPath, parseChecksums, releaseTag, sha256Hex } from './platform';

test('assetName maps every released platform/arch', () => {
  assert.equal(assetName('linux', 'x64'), 'thrift-ls-linux-amd64');
  assert.equal(assetName('linux', 'arm64'), 'thrift-ls-linux-arm64');
  assert.equal(assetName('darwin', 'x64'), 'thrift-ls-darwin-amd64');
  assert.equal(assetName('darwin', 'arm64'), 'thrift-ls-darwin-arm64');
  assert.equal(assetName('win32', 'x64'), 'thrift-ls-windows-amd64.exe');
  assert.equal(assetName('win32', 'arm64'), 'thrift-ls-windows-arm64.exe');
});

test('assetName is undefined for unsupported platforms', () => {
  assert.equal(assetName('freebsd', 'x64'), undefined);
  assert.equal(assetName('linux', 'ia32'), undefined);
});

test('findOnPath searches PATH in order', () => {
  const existing = new Set(['/usr/bin/thrift-ls', '/usr/local/bin/thrift-ls']);
  const found = findOnPath({
    platform: 'linux',
    pathEnv: '/usr/bin:/usr/local/bin:/opt/bin',
    sep: '/',
    delimiter: ':',
    exists: (p) => existing.has(p),
  });
  assert.equal(found, '/usr/bin/thrift-ls');
});

test('findOnPath tries PATHEXT extensions on windows', () => {
  const existing = new Set(['C:\\tools\\thrift-ls.exe']);
  const found = findOnPath({
    platform: 'win32',
    pathEnv: 'C:\\tools;C:\\other',
    pathext: '.EXE;.CMD',
    sep: '\\',
    delimiter: ';',
    exists: (p) => existing.has(p),
  });
  assert.equal(found, 'C:\\tools\\thrift-ls.exe');
});

test('findOnPath returns undefined when nothing matches', () => {
  const found = findOnPath({
    platform: 'linux',
    pathEnv: '/usr/bin:/opt/bin',
    sep: '/',
    delimiter: ':',
    exists: () => false,
  });
  assert.equal(found, undefined);
});

test('parseChecksums maps filenames to hashes', () => {
  const checksums = parseChecksums(
    'aa11  thrift-ls-linux-amd64\nbb22  thrift-ls-linux-arm64\n'
  );
  assert.equal(checksums.get('thrift-ls-linux-amd64'), 'aa11');
  assert.equal(checksums.get('thrift-ls-linux-arm64'), 'bb22');
  assert.equal(checksums.get('missing'), undefined);
});

test('parseChecksums ignores malformed lines', () => {
  const checksums = parseChecksums('not-a-checksum-line\n\ncc33  thrift-ls-darwin-amd64\n');
  assert.equal(checksums.size, 1);
  assert.equal(checksums.get('thrift-ls-darwin-amd64'), 'cc33');
});

test('sha256Hex matches known digests', () => {
  assert.equal(
    sha256Hex(Buffer.from('')),
    'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855'
  );
  assert.equal(
    sha256Hex(Buffer.from('thrift-ls')),
    '4c43bf971bd110e5ff6122e6f10ab8730559f8459f7a13757df0960c0b9ccc56'
  );
});

test('releaseTag pins dev builds to their commit release', () => {
  assert.equal(releaseTag('0.1.0-dev.110abea'), 'dev-110abea');
  assert.equal(releaseTag('0.1.0-dev.9984fd1'), 'dev-9984fd1');
});

test('releaseTag is undefined for stable or unknown versions', () => {
  assert.equal(releaseTag('0.1.0'), undefined);
  assert.equal(releaseTag('0.2.0-beta.1'), undefined);
  assert.equal(releaseTag(undefined), undefined);
  assert.equal(releaseTag(''), undefined);
});
