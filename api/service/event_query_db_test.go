package service

import (
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
)

func TestListEventsQuery(t *testing.T) {
	t.Parallel()
	f := newEventFixture(t)
	orgID := uuid.MustParse(f.orgID)
	user := func(id uuid.UUID, name, email string) uuid.UUID {
		if err := f.pool.QueryRow(t.Context(), "INSERT INTO users (id, name, email) VALUES ($1, $2, $3) RETURNING id",
			id, name, email).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	ada := user(uuid.MustParse("00000000-0000-4000-8000-000000000001"), "Ada Lovelace", "ada@acme.test")
	obrien := user(uuid.MustParse("00000000-0000-4000-8000-000000000002"), `O'Brien \ "Q"`, "ob@acme.test")
	domainID := uuid.MustParse("00000000-0000-4000-8000-000000000003")
	requestID := "11111111-1111-4111-8111-111111111111"
	insert := func(ev genDb.InsertEventParams) int64 {
		ev.OrgID = &orgID
		seq, err := f.queries.InsertEvent(t.Context(), ev)
		if err != nil {
			t.Fatal(err)
		}
		return seq
	}
	secret := insert(genDb.InsertEventParams{
		Type: events.SecretSet, ActorType: string(genDb.EntityTypeUser), ActorID: &ada, RequestID: &requestID,
		Data: []byte(`{"names": ["DB_URL", "100%_off"]}`),
	})
	domain := insert(genDb.InsertEventParams{
		Type: events.DomainCreated, ActorType: events.ActorSystem, SubjectType: new(events.SubjectDomain),
		SubjectID: &domainID, Data: []byte(`{"domain": "shop.example.com"}`),
	})
	member := insert(genDb.InsertEventParams{
		Type: events.MemberAdded, ActorType: string(genDb.EntityTypeUser), ActorID: &obrien,
		SubjectType: new(events.SubjectUser), SubjectID: &ada, Data: []byte(`{}`),
	})
	degraded := insert(genDb.InsertEventParams{
		Type: events.ResourceDegraded, ActorType: "agent", Data: []byte(`{"name": "a_b"}`),
	})
	updated := insert(genDb.InsertEventParams{
		Type: events.ResourceUpdated, ActorType: events.ActorSystem, Data: []byte(`{"name": "aXb"}`),
	})
	for query, want := range map[string][]int64{
		"":                                   {secret, domain, member, degraded, updated},
		"ada":                                {secret, member},
		"LOVELACE":                           {secret, member},
		"@actor:ada*":                        {secret},
		"@actor:ADA@ACME.TEST":               {secret},
		"@actor:ada":                         nil,
		"@actor:" + ada.String():             {secret},
		"@target:ada@acme.test":              {member},
		"@target:" + ada.String():            {member},
		"@target:" + domainID.String():       {domain},
		"@target:shop.example.com":           {domain},
		"@target:db_url":                     {secret},
		"shop":                               {domain},
		"db_url":                             {secret},
		"%":                                  {secret},
		"100%":                               {secret},
		"_":                                  {secret, degraded},
		"a_b":                                {degraded},
		"@target:a_b*":                       {degraded},
		`"O'Brien"`:                          {member},
		`\`:                                  {member},
		`"\\ \"Q\""`:                         {member},
		"@actor:o'brien*":                    {member},
		"SECRET":                             {secret},
		"@type:resource.*":                   {degraded, updated},
		"-@type:resource.*":                  {secret, domain, member},
		"@type:Resource.*":                   nil,
		"@type:resource.%":                   nil,
		"@actor_type:system":                 {domain, updated},
		"@actor_type:user -@actor:ada*":      {member},
		requestID:                            {secret},
		"@request:" + requestID:              {secret},
		requestID[:8]:                        nil,
		"-@target:ada*":                      {secret, domain, degraded, updated},
		"-@request:" + requestID:             {domain, member, degraded, updated},
		"ada OR shop":                        {secret, domain, member},
		"-(ada OR @actor_type:agent)":        {domain, updated},
		`"'; DROP TABLE events; --"`:         nil,
		`@target:"x' OR '1'='1"`:             nil,
		`@actor:"\\" OR @target:"%" -"_"`:    nil,
		"@type:" + events.SecretSet + " ada": {secret},
	} {
		// The fixture's org.created event stays out of every result.
		scoped := "-@type:" + events.OrgCreated
		if query != "" {
			scoped += " (" + query + ")"
		}
		res, err := f.events.ListEvents(f.owner, &eventv1.ListEventsRequest{OrgId: f.orgID, Query: scoped})
		if err != nil {
			t.Errorf("ListEvents(%q): %v", query, err)
			continue
		}
		var got []int64
		for _, event := range res.GetEvents() {
			got = append(got, event.GetSeq())
		}
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("ListEvents(%q) = %v, want %v", query, got, want)
		}
	}
	all, err := f.events.ListEvents(f.owner, &eventv1.ListEventsRequest{OrgId: f.orgID})
	if err != nil || len(all.GetEvents()) != 6 {
		t.Fatalf("events after the injection-shaped queries = %v, %v", all, err)
	}
}
