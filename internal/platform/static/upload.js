// The combat-log uploader (spec 003).
//
// THIS IS THE ONE PLACE THE SITE NEEDS A SCRIPT TO DO ITS JOB. A raid
// night's combat log is one to three gigabytes of text. It has to be
// compressed before it travels, cut into pieces so a dropped connection
// costs one piece and not the night, and resumed where it stopped -- and a
// plain form can do none of that. The constitution admits JavaScript where a
// specific interaction genuinely requires it; this is that interaction.
//
// What it does, and no more:
//
//   1. Fingerprints the file: SHA-256 over its first and last MiB and its
//      size, so a file already parsed is recognised before a byte is sent.
//   2. Asks the server to begin (or resume) the upload.
//   3. Cuts the file into pieces, compresses each with the browser's own
//      CompressionStream, and PUTs them in order. Each piece is a complete
//      gzip member; appended, they are one gzip file.
//   4. On a network error, asks the server how many pieces it holds and
//      carries on from there. On a refusal, stops and shows the message.
//   5. Tells the server the last piece is in, and goes to the upload's page.
//
// No library. Everything used here is in every current browser.

(function () {
  "use strict";

  var form = document.getElementById("upload-form");
  if (!form) return;

  var input = document.getElementById("upload-file");
  var button = document.getElementById("upload-button");
  var progress = document.getElementById("upload-progress");
  var status = document.getElementById("upload-status");
  var csrf = form.getAttribute("data-csrf");
  var beginURL = form.getAttribute("data-begin");
  var resumeID = form.getAttribute("data-resume");

  var supported = typeof CompressionStream === "function" && window.crypto && crypto.subtle && typeof fetch === "function";
  if (!supported) {
    say("This browser cannot compress the file before sending it. A current Chrome, Firefox, Edge or Safari can.", true);
    button.disabled = true;
    return;
  }
  say("Pick the game's WoWCombatLog.txt and press Upload.");

  form.addEventListener("submit", function (ev) {
    ev.preventDefault();
    var file = input.files && input.files[0];
    if (!file) {
      say("Pick a file first.", true);
      return;
    }
    button.disabled = true;
    input.disabled = true;
    upload(file).catch(function (err) {
      say(err && err.message ? err.message : "The upload stopped.", true);
      button.disabled = false;
      input.disabled = false;
    });
  });

  function say(text, isError) {
    status.textContent = text;
    status.classList.toggle("is-error", !!isError);
  }

  function headers(extra) {
    var h = { "X-CSRF-Token": csrf };
    for (var k in extra) h[k] = extra[k];
    return h;
  }

  async function fingerprint(file) {
    var mib = 1 << 20;
    var head = file.slice(0, Math.min(mib, file.size));
    var tail = file.slice(Math.max(0, file.size - mib), file.size);
    var parts = [await head.arrayBuffer(), await tail.arrayBuffer(), new TextEncoder().encode(String(file.size))];
    var total = parts.reduce(function (n, p) { return n + p.byteLength; }, 0);
    var all = new Uint8Array(total);
    var at = 0;
    parts.forEach(function (p) { all.set(new Uint8Array(p), at); at += p.byteLength; });
    var digest = await crypto.subtle.digest("SHA-256", all);
    return Array.from(new Uint8Array(digest)).map(function (b) { return b.toString(16).padStart(2, "0"); }).join("");
  }

  async function begin(file, fp) {
    var res = await fetch(beginURL, {
      method: "POST",
      headers: headers({ "Content-Type": "application/json" }),
      body: JSON.stringify({ filename: file.name, size: file.size, fingerprint: fp })
    });
    var body = await res.json().catch(function () { return {}; });
    if (!res.ok) throw new Error(body.error || "The upload could not be started.");
    return body;
  }

  async function compress(blob) {
    var stream = blob.stream().pipeThrough(new CompressionStream("gzip"));
    return new Response(stream).arrayBuffer();
  }

  async function upload(file) {
    say("Reading the file…");
    var fp = await fingerprint(file);
    var info = await begin(file, fp);
    if (info.state && info.state !== "receiving") {
      // Already parsed, or parsing: nothing to send.
      say("This file has already been uploaded. Taking you to it…");
      window.location.assign(info.url);
      return;
    }
    var id = info.id;
    var pieceBytes = info.piece_bytes;
    var total = info.pieces_total;
    var next = info.pieces_received || 0;
    progress.hidden = false;
    progress.max = total;

    while (next < total) {
      progress.value = next;
      say("Uploading piece " + (next + 1) + " of " + total + "…");
      var start = next * pieceBytes;
      var body = await compress(file.slice(start, Math.min(start + pieceBytes, file.size)));
      var res;
      try {
        res = await fetch(beginURL + "/" + id + "/pieces/" + next, {
          method: "PUT",
          headers: headers({ "Content-Type": "application/gzip" }),
          body: body
        });
      } catch (netErr) {
        // The connection dropped. Ask where we are and carry on.
        say("Connection lost; resuming…");
        await sleep(2000);
        var again = await begin(file, fp);
        next = again.pieces_received || 0;
        continue;
      }
      var reply = await res.json().catch(function () { return {}; });
      if (res.status === 409) {
        next = reply.pieces_received || 0;
        continue;
      }
      if (!res.ok) throw new Error(reply.error || "The upload was refused.");
      next = reply.pieces_received;
    }
    progress.value = total;

    say("Finishing…");
    var fin = await fetch(beginURL + "/" + id + "/finish", { method: "POST", headers: headers({}) });
    var finBody = await fin.json().catch(function () { return {}; });
    if (!fin.ok) throw new Error(finBody.error || "The upload could not be finished.");
    say("Uploaded. Parsing…");
    window.location.assign(finBody.url);
  }

  function sleep(ms) {
    return new Promise(function (resolve) { setTimeout(resolve, ms); });
  }

  // A page that lists an unfinished upload asks the member to pick the same
  // file again; begin() then answers with the pieces already held and the
  // loop continues from there. Nothing special to do here beyond making
  // that clear.
  if (resumeID) say("Pick the same file again to continue the unfinished upload.");
})();
