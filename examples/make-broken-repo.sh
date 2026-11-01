#!/usr/bin/env bash
# Creates a Git repository with the most common hook problems, so you can see
# what GitHookDoctor reports:
#   ./examples/make-broken-repo.sh /tmp/broken-hooks && githookdoctor diagnose -C /tmp/broken-hooks
set -euo pipefail
dir="${1:-broken-hooks-demo}"
rm -rf "$dir" && mkdir -p "$dir" && cd "$dir"
git init -q
git config core.autocrlf false
mkdir -p .husky/_ .githooks

# Husky v9 project with a leftover v4 config and the deprecated `husky install`.
cat > package.json <<'JSON'
{
  "name": "demo",
  "scripts": {
    "prepare": "husky install",
    "test": "node -e 1"
  },
  "devDependencies": {
    "husky": "^9.1.7"
  },
  "husky": {
    "hooks": { "pre-commit": "npm test" }
  }
}
JSON

# A hook saved with Windows line endings that still sources husky.sh.
printf '#!/usr/bin/env sh\r\n. "$(dirname -- "$0")/_/husky.sh"\r\n\r\nnpx lint-staged\r\n' > .husky/pre-commit
printf 'npx --no -- commitlint --edit "$1"\n' > .husky/commit-msg
# A typo in the hook name: Git only runs files named exactly like a hook.
printf 'npm test\n' > .husky/prepush

# Lefthook with a misspelled hook key.
cat > lefthook.yml <<'YAML'
precommit:
  commands:
    lint:
      run: npm run lint
pre-push:
  commands:
    test:
      run: npm test
YAML

# pre-commit with deprecated stage names.
cat > .pre-commit-config.yaml <<'YAML'
repos:
  - repo: https://github.com/pre-commit/pre-commit-hooks
    rev: v5.0.0
    hooks:
      - id: trailing-whitespace
      - id: no-commit-to-branch
        stages: [commit, push]
YAML

# An old hook in .git/hooks and a hooks path that points nowhere.
printf '#!/bin/sh\necho old hook\n' > .git/hooks/pre-push
chmod +x .git/hooks/pre-push
printf '#!/bin/sh\necho hi\n' > .githooks/commit-msg.sh
git add -A
git config core.hooksPath .githooks-missing
git config alias.yolo "commit --no-verify"
echo "Created $(pwd)"
