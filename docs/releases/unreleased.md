# Unreleased

- Fixed upstream Responses and Claude Code Messages SSE parsing to accept both
  `data: {...}` and `data:{...}`, along with optional spacing after `event:`.
- Fixed an EOF loop in Responses streaming when the upstream closes without a
  terminal event, including empty or comment-only streams.
