// Copyright 2018 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package main

import (
	"context"
	"fmt"
	"regexp"
)

var psycopgReleaseTagRegex = regexp.MustCompile(`^(?P<major>\d+)(?:_(?P<minor>\d+)(?:_(?P<point>\d+)(?:_(?P<subpoint>\d+))?)?)?$`)

// TODO(rafi): use a release tag once the commit below appears in a release.
var supportedPsycopgTag = "cecff195fc17a83d593dd62c239aa188883a844e"

// This test runs psycopg full test suite against a single cockroach node.
func registerPsycopg(r *testRegistry) {
	runPsycopg := func(
		ctx context.Context,
		t *test,
		c *cluster,
	) {
		if c.isLocal() {
			t.Fatal("cannot be run in local mode")
		}
		node := c.Node(1)
		t.Status("setting up cockroach")
		c.Put(ctx, cockroach, "./cockroach", c.All())
		c.Start(ctx, t, c.All())

		version, err := fetchCockroachVersion(ctx, c, node[0])
		if err != nil {
			t.Fatal(err)
		}

		if err := alterZoneConfigAndClusterSettings(ctx, version, c, node[0]); err != nil {
			t.Fatal(err)
		}

		t.Status("cloning psycopg and installing prerequisites")
		latestTag, err := repeatGetLatestTag(ctx, c, "psycopg", "psycopg2", psycopgReleaseTagRegex)
		if err != nil {
			t.Fatal(err)
		}
		c.l.Printf("Latest Psycopg release is %s.", latestTag)
		c.l.Printf("Supported Psycopg release is %s.", supportedPsycopgTag)

		if err := repeatRunE(
			ctx, c, node, "update apt-get", `sudo apt-get -qq update`,
		); err != nil {
			t.Fatal(err)
		}

		if err := repeatRunE(
			ctx,
			c,
			node,
			"install dependencies",
			`sudo apt-get -qq install make python3 libpq-dev python-dev gcc python3-setuptools python-setuptools`,
		); err != nil {
			t.Fatal(err)
		}

		if err := repeatRunE(
			ctx, c, node, "remove old Psycopg", `sudo rm -rf /mnt/data1/psycopg`,
		); err != nil {
			t.Fatal(err)
		}

		if err := repeatGitCloneE(
			ctx,
			t.l,
			c,
			"https://github.com/psycopg/psycopg2.git",
			"/mnt/data1/psycopg",
			"master",
			node,
		); err != nil {
			t.Fatal(err)
		}

		// TODO(rafi): once there's a real release, change the clone step above, and
		// remove this.
		if err := repeatRunE(
			ctx,
			c,
			node,
			"checkout supported tag",
			fmt.Sprintf(`cd /mnt/data1/psycopg/ && git fetch --depth=10000 && git checkout %s`, supportedPsycopgTag),
		); err != nil {
			t.Fatal(err)
		}

		t.Status("building Psycopg")
		if err := repeatRunE(
			ctx, c, node, "building Psycopg", `cd /mnt/data1/psycopg/ && make`,
		); err != nil {
			t.Fatal(err)
		}

		blocklistName, expectedFailures, ignoredlistName, ignoredlist := psycopgBlocklists.getLists(version)
		if expectedFailures == nil {
			t.Fatalf("No psycopg blocklist defined for cockroach version %s", version)
		}
		if ignoredlist == nil {
			t.Fatalf("No psycopg ignorelist defined for cockroach version %s", version)
		}
		c.l.Printf("Running cockroach version %s, using blocklist %s, using ignoredlist %s",
			version, blocklistName, ignoredlistName)

		t.Status("running psycopg test suite")
		// Note that this is expected to return an error, since the test suite
		// will fail. And it is safe to swallow it here.
		rawResults, _ := c.RunWithBuffer(ctx, t.l, node,
			`cd /mnt/data1/psycopg/ &&
			export PSYCOPG2_TESTDB=defaultdb &&
			export PSYCOPG2_TESTDB_USER=root &&
			export PSYCOPG2_TESTDB_PORT=26257 &&
			export PSYCOPG2_TESTDB_HOST=localhost &&
			make check`,
		)

		t.Status("collating the test results")
		c.l.Printf("Test Results: %s", rawResults)

		// Find all the failed and errored tests.
		results := newORMTestsResults()
		results.parsePythonUnitTestOutput(rawResults, expectedFailures, ignoredlist)
		results.summarizeAll(
			t, "psycopg" /* ormName */, blocklistName, expectedFailures,
			version, supportedPsycopgTag,
		)
	}

	r.Add(testSpec{
		Name:       "psycopg",
		Owner:      OwnerAppDev,
		Cluster:    makeClusterSpec(1),
		MinVersion: "v19.1.0",
		Tags:       []string{`default`, `driver`},
		Run: func(ctx context.Context, t *test, c *cluster) {
			runPsycopg(ctx, t, c)
		},
	})
}
