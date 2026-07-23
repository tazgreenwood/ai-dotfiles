#!/usr/bin/env python3
"""Standalone CLI wrapper around Headroom compression for the
inject-registry-context.js hook.

Reads all of stdin as text, wraps it as a single user message, and runs
it through `headroom.compress()` using the invocation validated in
DOTFILES-13 (see tools/spikes/headroom_benchmark.py). Writes the
compressed text to stdout.

Any failure at all (headroom-ai not installed, internal Headroom error,
anything else) is caught and the script falls back to writing the
original, uncompressed text back to stdout unchanged. This script must
never exit non-zero and must never write to stderr in a way that
pollutes stdout — the calling hook treats stdout as the sole output.
"""

import sys


def main() -> None:
    text = sys.stdin.read()

    try:
        import headroom

        messages = [{"role": "user", "content": text}]
        result = headroom.compress(
            messages,
            model="gpt-4o",
            compress_user_messages=True,
            protect_recent=0,
        )
        output = result.messages[0]["content"]
    except Exception:
        output = text

    sys.stdout.write(output)


if __name__ == "__main__":
    main()
