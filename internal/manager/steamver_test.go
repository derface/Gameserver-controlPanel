package manager

import "testing"

func TestParseLatestBuild(t *testing.T) {
	out := `"896660"
{
	"common" { "name" "Valheim Dedicated Server" }
	"depots"
	{
		"896661" { "manifests" { "public" { "gid" "1" } } }
		"branches"
		{
			"public"
			{
				"buildid"		"20456789"
				"timeupdated"		"1760000000"
			}
			"public-test"
			{
				"buildid"		"20500000"
			}
		}
	}
}`
	if b := parseLatestBuild(out, "public"); b != "20456789" {
		t.Fatal(b)
	}
	if b := parseLatestBuild(out, "public-test"); b != "20500000" {
		t.Fatal(b)
	}
	if b := parseLatestBuild("nothing", "public"); b != "" {
		t.Fatal(b)
	}
}
