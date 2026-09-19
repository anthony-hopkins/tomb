package ai

import (
	"errors"
	"strings"
	"testing"
)

const goodRaidReport = "```json\n" + `{"overview":"Two kills and a wall.","bosses":[{"name":"Nymrissa Wavecaller","summary":"s","tanks":"t","healers":"h","dps":"d","positioning":"p","mechanics":"m","adds":"a","wipes":"Killed first pull."},{"name":"Entombed Sentinels","summary":"s","tanks":"t","healers":"h","dps":"d","positioning":"","mechanics":"m","adds":"a","wipes":"### The plan for the next pull\n1. Tanks swap at three stacks.","tank_plan":"### Cwoodz\nDemon Spikes before Empowering Slam.","healer_plan":"","dps_plan":"### Trogdoor\nDisintegrate 8.1 a minute against 12.4."}],"do_these_first":["one","two","three"],"verify":[{"item":"yards","status":"inferred","note":"the log's scale"}]}` + "\n```"

// TestParseRaidReport: the fence is tolerated, the boss count checked,
// three things first required, empty sections left out of the order.
func TestParseRaidReport(t *testing.T) {
	r, err := ParseRaidReport(goodRaidReport, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Bosses) != 2 || r.Bosses[1].Name != "Entombed Sentinels" || len(r.Verify) != 1 {
		t.Errorf("report = %+v", r)
	}
	secs := r.Bosses[1].Sections()
	if len(secs) != 9 || secs[0].Title != "Against the top kill" || secs[2].Key != "tank_plan" || secs[len(secs)-1].Key != "wipes" {
		t.Errorf("sections = %+v", secs)
	}
	for _, s := range secs {
		if s.Key == "positioning" {
			t.Error("an empty section was kept")
		}
	}
	if _, err := ParseRaidReport(goodRaidReport, 3); !errors.Is(err, ErrBadRaidReport) {
		t.Errorf("wrong boss count err = %v", err)
	}
	if _, err := ParseRaidReport(strings.Replace(goodRaidReport, `"one","two","three"`, `"one"`, 1), 2); !errors.Is(err, ErrBadRaidReport) {
		t.Errorf("one thing first err = %v", err)
	}
	if _, err := ParseRaidReport("not json", 0); !errors.Is(err, ErrBadRaidReport) {
		t.Errorf("garbage err = %v", err)
	}
}

// TestWarRoomInstruction: the instruction asks for what the officers want
// -- every role criticised, positioning, mechanics, adds, the wall's plan
// -- and the schema requires every section.
func TestWarRoomInstruction(t *testing.T) {
	for _, want := range []string{"- tanks:", "- healers:", "- dps:", "- positioning:", "- mechanics:", "- adds:", "- wipes:", "- tank_plan:", "- healer_plan:", "- dps_plan:", "Demon Spikes before Empowering Slam", "flex to damage", "why is their damage low", "The plan for the next pull", "exactly three strings", "never recompute", `"avoidable"`, `"rotations"`, "Never invent a player"} {
		if !strings.Contains(SystemWarRoom, want) {
			t.Errorf("instruction is missing %q", want)
		}
	}
	items := RaidReportSchema["properties"].(map[string]any)["bosses"].(map[string]any)["items"].(map[string]any)
	req := items["required"].([]string)
	if len(req) != 12 {
		t.Errorf("boss sections required = %v", req)
	}
	msg := BuildWarRoom(map[string]any{"report": "ABC", "bosses": []string{}})
	if !strings.Contains(msg, "## night") || !strings.Contains(msg, `"report": "ABC"`) {
		t.Errorf("message = %q", msg)
	}
}
