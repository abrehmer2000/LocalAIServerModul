// Run with: node scripts/dashboard_stream_test.cjs
// Exercise the dashboard's actual stream reader with single-byte chunks.
const fs = require("node:fs");
const path = require("node:path");
const assert = require("node:assert/strict");
const source = fs.readFileSync(
  path.join(__dirname, "../internal/webui/assets/app.js"),
  "utf8",
);
const start = source.indexOf("      const reader = response.body.getReader();");
assert.ok(start >= 0);
const end = source.indexOf("    } catch (error) {", start);
assert.ok(end > start);
const AsyncFunction = Object.getPrototypeOf(async function () {}).constructor;
const run = new AsyncFunction(
  "response",
  "byId",
  "TextDecoder",
  "controller",
  "let session=1,current=1,done=false,finishReason=null;" +
    source.slice(start, end),
);

(async () => {
  let count = 0;
  for (const separator of ["\n", "\r\n", "\r"]) {
    for (const kind of ["text", "empty", "length", "unfinished"]) {
      const event = (data) =>
        "data: " + JSON.stringify(data) + separator + separator;
      const delta =
        kind === "text"
          ? { content: "Grüße 🌈" }
          : { reasoning_content: "hidden" };
      let wire = event({ choices: [{ delta, finish_reason: null }] });
      wire += event({
        choices: [
          { delta: {}, finish_reason: kind === "length" ? "length" : "stop" },
        ],
      });
      if (kind !== "unfinished") wire += "data: [DONE]" + separator + separator;
      const bytes = new TextEncoder().encode(wire);
      let position = 0;
      const dom = {
        "test-answer": { textContent: "" },
        "test-message": { textContent: "" },
      };
      const response = {
        body: {
          getReader: () => ({
            read: async () =>
              position < bytes.length
                ? { value: bytes.slice(position, ++position), done: false }
                : { done: true },
          }),
        },
      };
      await run(response, (id) => dom[id], TextDecoder, {
        abort() {
          throw Error("Unexpected abort");
        },
      });
      assert.equal(
        dom["test-answer"].textContent,
        kind === "text" ? "Grüße 🌈" : "",
      );
      const expected = {
        text: /vollständig/,
        empty: /keine Textantwort/,
        length: /Ausgabelimit/,
        unfinished: /ohne Abschluss/,
      };
      assert.match(dom["test-message"].textContent, expected[kind]);
      count++;
    }
  }
  console.log(`${count} dashboard stream cases passed.`);
})().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
