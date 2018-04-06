// Copyright 2018 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package internal

import (
	"log"
	"strings"

	errgo "gopkg.in/errgo.v1"
	"gopkg.in/macaroon-bakery.v2/bakery"
	mgo "gopkg.in/mgo.v2"

	"github.com/CanonicalLtd/candid/cmd/migrate-db/internal/mongodoc"
	"github.com/CanonicalLtd/candid/store"
)

// A LegacySource is a Source from a legacy mgo store.
type LegacySource struct {
	db       *mgo.Database
	identity *store.Identity
	iter     *mgo.Iter
	err      error
}

// NewLegacySource creates a LegacySource from the given database.
func NewLegacySource(db *mgo.Database) *LegacySource {
	return &LegacySource{
		db: db,
	}
}

// Next implements Source.Next.
func (s *LegacySource) Next() bool {
	if s.iter == nil {
		s.iter = s.db.C("identities").Find(nil).Iter()
	}
	for {
		var doc mongodoc.Identity
		if !s.iter.Next(&doc) {
			return false
		}
		var err error
		s.identity, err = convert(&doc)
		if err != nil {
			log.Printf("cannot convert identity (skipping): %s", err)
			continue
		}
		return true
	}
}

func convert(doc *mongodoc.Identity) (*store.Identity, error) {
	providerID := providerID(doc)
	if providerID == "" {
		return nil, errgo.Newf("unrecognised external ID %q", doc.ExternalID)
	}
	identity := &store.Identity{
		Username:   doc.Username,
		ProviderID: providerID,
		Name:       doc.FullName,
		Email:      doc.Email,
		Groups:     doc.Groups,
	}
	if doc.LastLogin != nil {
		identity.LastLogin = *doc.LastLogin
	}
	if doc.LastDischarge != nil {
		identity.LastDischarge = *doc.LastDischarge
	}
	for _, k := range doc.PublicKeys {
		var key bakery.Key
		copy(key[:], k.Key)
		identity.PublicKeys = append(identity.PublicKeys, bakery.PublicKey{key})
	}
	if doc.Owner != "" {
		if doc.Owner == "admin@idm" {
			identity.ProviderInfo = map[string][]string{
				"owner": {string(store.MakeProviderIdentity("idm", "admin@idm")), "admin@idm"},
			}
		} else {
			return nil, errgo.Newf("unrecognised owner for %s (%q)", doc.Username, doc.Owner)
		}
	}
	if len(doc.SSHKeys) > 0 {
		identity.ExtraInfo = map[string][]string{
			"sshkeys": doc.SSHKeys,
		}
	}
	return identity, nil
}

func providerID(doc *mongodoc.Identity) store.ProviderIdentity {
	if doc.ExternalID == "" {
		return store.MakeProviderIdentity("idm", doc.Username)
	}
	if strings.HasPrefix(doc.ExternalID, "https://login.ubuntu.com/+id") {
		return store.MakeProviderIdentity("usso", doc.ExternalID)
	}
	if strings.HasPrefix(doc.ExternalID, "openid-connect:") {
		// The only currently used openid provider is azure
		return store.MakeProviderIdentity("azure", strings.TrimPrefix(doc.ExternalID, "openid-connect:"))
	}
	if strings.HasPrefix(doc.ExternalID, "usso-openid:") {
		return store.MakeProviderIdentity("usso_macaroon", strings.TrimPrefix(doc.ExternalID, "usso-openid:"))
	}
	return ""
}

// Identity implements Source.Identity.
func (s *LegacySource) Identity() *store.Identity {
	return s.identity
}

// Err implements Source.Err.
func (s *LegacySource) Err() error {
	return errgo.Mask(s.iter.Err())
}
