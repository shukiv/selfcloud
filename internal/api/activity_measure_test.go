// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/mail"
	"github.com/shukiv/selfcloud/internal/store"
)

// TestMeasureActivityPageWithManyActors measures one request of the route at limit=500: a file at the bottom of a
// directory tree 60 levels deep, 500 entries of it made by 45 distinct members who each hold a role on the file. It times
// the route with the bound on the reach walks (40, the default) and with the bound lifted, as the median of 15 reads. It
// runs only when SELFCLOUD_MEASURE_ACTIVITY is set.
func TestMeasureActivityPageWithManyActors(t *testing.T) {
	if os.Getenv("SELFCLOUD_MEASURE_ACTIVITY") == "" {
		t.Skip("set SELFCLOUD_MEASURE_ACTIVITY=1 to run the activity page measurement")
	}
	s := newAuthServer(t, &mail.Recorder{})
	admin := adminKey(t, s)
	acc, key := newAccount(t, s, admin, "Acme")
	s.limits["activity"] = newLimiter(100000)
	s.limits["activity.account"] = newLimiter(100000)
	f := newFolder(t, s, key, acc.ID, "Deep")
	path := ""
	for i := 0; i < 60; i++ {
		path += fmt.Sprintf("/d%d", i)
	}
	mkdirs(t, s, key, f.ID, path)
	file := putNode(t, s, key, f.ID, path+"/file.txt", "x")
	reader, readerSess := verifiedUser(t, s, key, acc.ID, "reader@example.com")
	wantStatus(t, call(t, s, "PUT", "/api/v1/nodes/"+file.ID+"/permissions/"+reader.ID, key, map[string]any{"role": "viewer"}), 200)
	var actors []string
	for i := 0; i < 45; i++ {
		u, _ := verifiedUser(t, s, key, acc.ID, fmt.Sprintf("member%d@example.com", i))
		wantStatus(t, call(t, s, "PUT", "/api/v1/nodes/"+file.ID+"/permissions/"+u.ID, key, map[string]any{"role": "editor"}), 200)
		actors = append(actors, u.ID)
	}
	err := s.store.Write(t.Context(), func(tx *store.Tx) error {
		for i := 0; i < 500; i++ {
			e := &store.AuditEntry{Time: time.Now(), AccountID: acc.ID, ActorKind: "user", ActorID: actors[i%len(actors)],
				Action: "node.write", Target: file.ID, Details: map[string]any{"size": 1}}
			if err := tx.InsertAudit(t.Context(), e); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	read := func(label string) {
		var d []time.Duration
		var named int
		for i := 0; i < 15; i++ {
			start := time.Now()
			rec := callSession(t, s, readerSess, "GET", "/api/v1/nodes/"+file.ID+"/activity?limit=500", nil)
			d = append(d, time.Since(start))
			wantStatus(t, rec, 200)
			named = 0
			for _, it := range decode[listResponse[activityJSON]](t, rec).Items {
				if it.Actor.Name != "" {
					named++
				}
			}
		}
		slices.Sort(d)
		t.Logf("%-34s median %8s  max %8s  entries with a name %d of 500", label, d[len(d)/2], d[len(d)-1], named)
	}
	s.activityWalks = 1
	read("bound 1 (one person walked)")
	s.activityWalks = 0
	read("bound 40 (default)")
	s.activityWalks = 1000
	read("bound lifted (45 people walked)")
}
