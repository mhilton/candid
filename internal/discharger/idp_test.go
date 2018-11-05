// Copyright 2017 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package discharger_test

import (
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"time"

	qt "github.com/frankban/quicktest"
	"golang.org/x/net/context"
	"gopkg.in/CanonicalLtd/candidclient.v1/params"
	errgo "gopkg.in/errgo.v1"
	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/httpbakery"

	"github.com/CanonicalLtd/candid/idp"
	"github.com/CanonicalLtd/candid/internal/auth"
	"github.com/CanonicalLtd/candid/internal/candidtest"
	"github.com/CanonicalLtd/candid/internal/discharger"
	"github.com/CanonicalLtd/candid/internal/identity"
	"github.com/CanonicalLtd/candid/internal/monitoring"
	"github.com/CanonicalLtd/candid/meeting"
	"github.com/CanonicalLtd/candid/store"
)

type idpSuite struct {
	store *candidtest.Store

	// template is used to configure the output generated by success
	// following a login. if there is a template called "login" in
	// template then it will be processed and the output returned.
	template     *template.Template
	meetingPlace *meeting.Place

	vc idp.VisitCompleter
}

func (s *idpSuite) Init(c *qt.C) {
	s.store = candidtest.NewStore()

	s.template = template.New("")

	oven := bakery.NewOven(bakery.OvenParams{
		Namespace: auth.Namespace,
		RootKeyStoreForOps: func([]bakery.Op) bakery.RootKeyStore {
			return s.store.BakeryRootKeyStore
		},
		Key:      bakery.MustGenerateKey(),
		Location: "candidtest",
	})
	var err error
	s.meetingPlace, err = meeting.NewPlace(meeting.Params{
		Store:      s.store.MeetingStore,
		Metrics:    monitoring.NewMeetingMetrics(),
		ListenAddr: "localhost",
	})
	c.Assert(err, qt.Equals, nil)
	c.Defer(s.meetingPlace.Close)

	kvs, err := s.store.ProviderDataStore.KeyValueStore(context.Background(), "test-discharge-tokens")
	c.Assert(err, qt.Equals, nil)
	s.vc = discharger.NewVisitCompleter(identity.HandlerParams{
		ServerParams: identity.ServerParams{
			Store:        s.store.Store,
			MeetingStore: s.store.MeetingStore,
			RootKeyStore: s.store.BakeryRootKeyStore,
			Template:     s.template,
		},
		MeetingPlace: s.meetingPlace,
		Oven:         oven,
	}, kvs)
}

func (s *idpSuite) TestLoginFailure(c *qt.C) {
	rr := httptest.NewRecorder()
	s.vc.Failure(context.Background(), rr, nil, "", errgo.WithCausef(nil, params.ErrForbidden, "test error"))
	c.Assert(rr.Code, qt.Equals, http.StatusForbidden)
	var perr params.Error
	err := json.Unmarshal(rr.Body.Bytes(), &perr)
	c.Assert(err, qt.Equals, nil)
	c.Assert(perr, qt.DeepEquals, params.Error{
		Code:    params.ErrForbidden,
		Message: "test error",
	})
}

func (s *idpSuite) TestLoginFailureWithWait(c *qt.C) {
	id := "test"
	err := s.meetingPlace.NewRendezvous(context.Background(), id, []byte("test"))
	c.Assert(err, qt.Equals, nil)

	rr := httptest.NewRecorder()
	s.vc.Failure(context.Background(), rr, nil, id, errgo.WithCausef(nil, params.ErrForbidden, "test error"))
	c.Assert(rr.Code, qt.Equals, http.StatusForbidden)
	var perr params.Error
	err = json.Unmarshal(rr.Body.Bytes(), &perr)
	c.Assert(err, qt.Equals, nil)
	c.Assert(perr, qt.DeepEquals, params.Error{
		Code:    params.ErrForbidden,
		Message: "test error",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	d1, d2, err := s.meetingPlace.Wait(ctx, id)
	c.Assert(err, qt.Equals, nil)
	c.Assert(string(d1), qt.Equals, "test")
	var li discharger.LoginInfo
	err = json.Unmarshal(d2, &li)
	c.Assert(err, qt.Equals, nil)
	c.Assert(li.DischargeToken, qt.IsNil)
	c.Assert(li.Error, qt.DeepEquals, &httpbakery.Error{
		Message: "test error",
	})
}

func (s *idpSuite) TestLoginSuccess(c *qt.C) {
	req, err := http.NewRequest("GET", "", nil)
	c.Assert(err, qt.Equals, nil)
	rr := httptest.NewRecorder()
	s.vc.Success(context.Background(), rr, req, "", &store.Identity{
		Username: "test-user",
	})
	c.Assert(rr.Code, qt.Equals, http.StatusOK)
	c.Assert(rr.HeaderMap.Get("Content-Type"), qt.Equals, "text/plain; charset=utf-8")
	c.Assert(rr.Body.String(), qt.Equals, "Login successful as test-user")
}

func (s *idpSuite) TestLoginSuccessWithTemplate(c *qt.C) {
	_, err := s.template.New("login").Parse("<h1>Login successful as {{.Username}}</h1>")
	c.Assert(err, qt.Equals, nil)
	req, err := http.NewRequest("GET", "", nil)
	c.Assert(err, qt.Equals, nil)
	rr := httptest.NewRecorder()
	s.vc.Success(context.Background(), rr, req, "", &store.Identity{
		Username: "test-user",
	})
	c.Assert(rr.Code, qt.Equals, http.StatusOK)
	c.Assert(rr.HeaderMap.Get("Content-Type"), qt.Equals, "text/html;charset=utf-8")
	c.Assert(rr.Body.String(), qt.Equals, "<h1>Login successful as test-user</h1>")
}

func (s *idpSuite) TestLoginRedirectSuccess(c *qt.C) {
	req, err := http.NewRequest("GET", "", nil)
	c.Assert(err, qt.Equals, nil)
	rr := httptest.NewRecorder()
	s.vc.RedirectSuccess(context.Background(), rr, req, "http://example.com/callback", "1234", &store.Identity{
		Username: "test-user",
	})
	resp := rr.Result()
	c.Assert(resp.StatusCode, qt.Equals, http.StatusTemporaryRedirect)
	loc, err := resp.Location()
	c.Assert(err, qt.Equals, nil)
	v := loc.Query()
	loc.RawQuery = ""
	c.Assert(loc.String(), qt.Equals, "http://example.com/callback")
	c.Assert(v.Get("state"), qt.Equals, "1234")
	c.Assert(v.Get("code"), qt.Not(qt.Equals), "")
}

func (s *idpSuite) TestLoginRedirectSuccessInvalidReturnTo(c *qt.C) {
	req, err := http.NewRequest("GET", "", nil)
	c.Assert(err, qt.Equals, nil)
	rr := httptest.NewRecorder()
	s.vc.RedirectSuccess(context.Background(), rr, req, "::", "1234", &store.Identity{
		Username: "test-user",
	})
	c.Assert(rr.Code, qt.Equals, http.StatusBadRequest)
	var perr params.Error
	err = json.Unmarshal(rr.Body.Bytes(), &perr)
	c.Assert(err, qt.Equals, nil)
	c.Assert(perr, qt.DeepEquals, params.Error{
		Code:    params.ErrBadRequest,
		Message: `invalid return_to: parse ::: missing protocol scheme`,
	})
}

func (s *idpSuite) TestLoginRedirectFailureInvalidReturnTo(c *qt.C) {
	req, err := http.NewRequest("GET", "", nil)
	c.Assert(err, qt.Equals, nil)
	rr := httptest.NewRecorder()
	s.vc.RedirectFailure(context.Background(), rr, req, "::", "1234", errgo.WithCausef(nil, params.ErrForbidden, "test error"))
	c.Assert(rr.Code, qt.Equals, http.StatusForbidden)
	var perr params.Error
	err = json.Unmarshal(rr.Body.Bytes(), &perr)
	c.Assert(err, qt.Equals, nil)
	c.Assert(perr, qt.DeepEquals, params.Error{
		Code:    params.ErrForbidden,
		Message: `test error`,
	})
}
