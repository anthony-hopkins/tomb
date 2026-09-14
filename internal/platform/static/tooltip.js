// Item tooltips: placement, and lighting up the pieces of a set.
//
// THIS FILE IS AN ENHANCEMENT, NOT A REQUIREMENT.
//
// The tooltips open and close in CSS alone, on :hover and :focus-within. With
// this script blocked, missing, or failing to parse, every tooltip still opens
// and still shows everything it shows now -- it is simply placed by CSS rather
// than by measurement. Nothing here is load-bearing, and nothing here should
// ever become load-bearing: the constitution admits JavaScript only where a
// specific interaction genuinely requires it, and "put this box where it fits"
// is the interaction that qualifies.
//
// It does two things:
//
//   1. Places an open tooltip so it stays inside the viewport. CSS can put it
//      beside its row, but it cannot know that the row is near the bottom of
//      the window and the box would be cut off.
//
//   2. Highlights the other pieces of a tier set while one of them is open,
//      which is the thing the game does and the thing you cannot do in CSS --
//      it means styling siblings based on a shared attribute value.
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

  function open(item) {
    var tip = item.querySelector(".item-tooltip");
    if (!tip) {
      return;
    }
    item.classList.add("tip-open");
    place(item, tip);
    markSet(item, true);
  }

  function close(item) {
    var tip = item.querySelector(".item-tooltip");
    if (tip) {
      // Hand placement back to the stylesheet, so a tooltip opened later
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
    // focusable element is the button inside the item, not the item itself.
    item.addEventListener("focusin", function () { open(item); });
    item.addEventListener("focusout", function () { close(item); });
  }

  function init() {
    var items = document.querySelectorAll(".gear-item");
    for (var i = 0; i < items.length; i++) {
      bind(items[i]);
    }

    // An open tooltip is placed in viewport coordinates, so it has to be
    // repositioned when the viewport moves under it.
    var reposition = function () {
      var open = document.querySelector(".gear-item.tip-open");
      if (open) {
        place(open, open.querySelector(".item-tooltip"));
      }
    };
    window.addEventListener("scroll", reposition, { passive: true });
    window.addEventListener("resize", reposition);

    // Escape closes, which is what a keyboard user expects of anything that
    // pops up.
    document.addEventListener("keydown", function (e) {
      if (e.key === "Escape") {
        var open = document.querySelector(".gear-item.tip-open");
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
