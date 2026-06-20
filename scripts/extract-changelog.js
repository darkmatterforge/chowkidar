#!/usr/bin/env node
// Usage: node scripts/extract-changelog.js <version> [changelog-path]
// Prints the body of the given version's entry from a Keep a Changelog file.
import parser from 'changelog-parser';

const version = process.argv[2];
const file = process.argv[3] || 'CHANGELOG.md';

if (!version) {
  process.stderr.write('Usage: extract-changelog.js <version> [changelog-path]\n');
  process.exit(1);
}

const result = await parser(file);
const entry = result.versions.find(v => v.version === version);
process.stdout.write(entry ? entry.body.trim() : '');
