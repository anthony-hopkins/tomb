// Popover placement: item tooltips, character cards, and the scrolling rail.
//
// THIS FILE IS AN ENHANCEMENT, NOT A REQUIREMENT.
//
// The tooltips and cards open and close in CSS alone, on :hover and
// :focus-within. With this script blocked, missing, or failing to parse, every
// one of them still opens and still shows everything it shows now -- it is
// simply placed by CSS rather than by measurement, and the character rail is
// the tall sticky column it always was. Nothing here is load-bearing, and
// nothing here should ever become load-bearing: the constitution admits
// JavaScript only where a specific interaction genuinely requires it, and "put
// this box where it fits" is the interaction that qualifies.
//
// It does four things:
//
//   1. Places an open tooltip or card so it stays inside the viewport. CSS can
//      put it beside its row, but it cannot know that the row is near the
//      bottom of the window and the box would be cut off.
//
//   2. Highlights the other pieces of a tier set while one of them is open,
//      which is the thing the game does and the thing you cannot do in CSS --
//      it means styling siblings based on a shared attribute value.
//
//   3. Turns the character rail into a pinned list that scrolls on its own.
//      A guild roster is taller than any window, and a sticky column taller
//      than the window simply scrolls away with the page. The rail could
//      scroll internally in CSS alone -- except that a scrolling box clips
//      anything positioned outside it, and the cards sit beside their rows,
//      outside the rail. So the rail scrolls only once this script is placing
//      the cards in viewport coordinates, where the rail cannot clip them.
//      The class it adds is what the stylesheet keys the scrolling on.
//
//   4. Submits the roster search the moment a name is PICKED from the
//      browser's suggestions. The suggestions are a native datalist, which
//      only ever fills the box; without this, picking a name still leaves
//      the viewer to press enter. Typing is left alone -- only a pick, or
//      a paste, of a name that is actually on the roster submits.
//
// No framework, no build step, no dependencies. It is served from the same
// origin under a script-src of 'self'.

(function () {
  "use strict";

  var MARGIN = 12; // keep this far from the viewport edge

  /** Position one tooltip next to its row, flipping and clamping to fit. */
  function place(item, tip) {
    // Clear any previous placement before measuring, or the measurement is of
    // the last position rather than the natural one.
    tip.style.left = "";
    tip.style.top = "";
    tip.style.right = "";

    var row = item.getBoundingClientRect();
    var box = tip.getBoundingClientRect();
    var vw = document.documentElement.clientWidth;
    var vh = document.documentElement.clientHeight;

    // Prefer to the right of the row; flip to the left when there is no room.
    var left = row.right + MARGIN;
    if (left + box.width > vw - MARGIN) {
      left = row.left - box.width - MARGIN;
    }
    // If neither side fits -- a narrow window -- sit against the left edge.
    if (left < MARGIN) {
      left = MARGIN;
    }

    // Vertically centre on the row, then clamp so it never runs off an edge.
    var top = row.top + row.height / 2 - box.height / 2;
    if (top + box.height > vh - MARGIN) {
      top = vh - box.height - MARGIN;
    }
    if (top < MARGIN) {
      top = MARGIN;
    }

    tip.style.position = "fixed";
    tip.style.left = Math.round(left) + "px";
    tip.style.top = Math.round(top) + "px";
  }

  /** Mark the other pieces of this item's set, if it belongs to one. */
  function markSet(item, on) {
    var set = item.getAttribute("data-set");
    if (!set) {
      return;
    }
    var siblings = document.querySelectorAll('.gear-item[data-set="' + set + '"]');
    for (var i = 0; i < siblings.length; i++) {
      siblings[i].classList.toggle("set-related", on && siblings[i] !== item);
    }
  }

  /** The popover an item owns: a gear row's tooltip, a character row's card. */
  function popoverOf(item) {
    return item.querySelector(".item-tooltip, .character-card.popover");
  }

  function open(item) {
    var tip = popoverOf(item);
    if (!tip) {
      return;
    }
    item.classList.add("tip-open");
    place(item, tip);
    markSet(item, true);
  }

  function close(item) {
    var tip = popoverOf(item);
    if (tip) {
      // Hand placement back to the stylesheet, so a popover opened later
      // without this script still lands somewhere sensible.
      tip.style.position = "";
      tip.style.left = "";
      tip.style.top = "";
    }
    item.classList.remove("tip-open");
    markSet(item, false);
  }

  function bind(item) {
    item.addEventListener("mouseenter", function () { open(item); });
    item.addEventListener("mouseleave", function () { close(item); });
    // focusin/focusout rather than focus/blur: those do not bubble, and the
    // focusable element may be a button or link inside the item.
    item.addEventListener("focusin", function () { open(item); });
    item.addEventListener("focusout", function () { close(item); });
  }

  function init() {
    var items = document.querySelectorAll(".gear-item, .character-row");
    for (var i = 0; i < items.length; i++) {
      bind(items[i]);
    }

    // An open popover is placed in viewport coordinates, so it has to be
    // repositioned when anything moves under it: the page, or the rail.
    var reposition = function () {
      var open = document.querySelector(".tip-open");
      if (open) {
        place(open, popoverOf(open));
      }
    };
    window.addEventListener("scroll", reposition, { passive: true });
    window.addEventListener("resize", reposition);

    // Now that cards are placed where the rail cannot clip them, the rail may
    // scroll on its own. The stylesheet does the rest off this class.
    // The roster search: a pick from the suggestions goes straight there.
    //
    // A keystroke reports itself as insertText or a delete*; a pick from the
    // datalist reports insertReplacementText (Chromium, Safari) or no
    // inputType at all (Firefox). So anything that is not typing, whose
    // result is exactly one of the suggestions, is treated as a choice made.
    // The exact-match check is what stops a typed prefix that happens to be
    // somebody's whole name from firing mid-word.
    var search = document.querySelector(".roster-search");
    var box = search && search.querySelector("input[list]");
    var list = box && document.getElementById(box.getAttribute("list"));
    if (search && box && list) {
      box.addEventListener("input", function (e) {
        var how = e.inputType || "";
        if (how === "insertText" || how.indexOf("delete") === 0) {
          return;
        }
        var opts = list.options;
        for (var i = 0; i < opts.length; i++) {
          if (opts[i].value === box.value) {
            search.submit();
            return;
          }
        }
      });
    }

    var nav = document.querySelector(".character-nav");
    if (nav) {
      nav.classList.add("is-scrollable");
      nav.addEventListener("scroll", reposition, { passive: true, capture: true });
    }

    // Escape closes, which is what a keyboard user expects of anything that
    // pops up.
    document.addEventListener("keydown", function (e) {
      if (e.key === "Escape") {
        var open = document.querySelector(".tip-open");
        if (open) {
          close(open);
        }
      }
    });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
