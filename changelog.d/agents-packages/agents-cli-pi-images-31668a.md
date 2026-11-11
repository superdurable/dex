---
section: Images
---
- Agents CLI: the TypeScript **dex-agents-pi** command passes the media store
  to the built-in loop, so images attached with **/image** reach its model.
  Images that its Pi tools return, such as `read` of a PNG, JPEG, GIF, or
  WebP file in the workspace, are uploaded to the same store in the tool Step
  and reach the next model call after the result's text, at most 20 per
  result. A refused or excess image becomes `[image omitted: <mime type>]`
  and the call still succeeds. Before, `read` gave the model only that note.
  See [cli/typescript/pi](cli/typescript/pi/README.md#images).
