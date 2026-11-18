-- Legacy OIDC rows carry no proof of the authority that issued their subject.
-- Owners can replace them through the authenticated connect flow.
ALTER TABLE oauth_identities ADD COLUMN issuer TEXT NOT NULL DEFAULT '';
DROP INDEX ux_oauth_identity_subject;
CREATE UNIQUE INDEX ux_oauth_identity_subject ON oauth_identities (provider, issuer, subject);
