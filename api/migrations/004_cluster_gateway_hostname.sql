-- Cross-region gateway failover needs each region's gateway to be addressable by a
-- publicly resolvable name, so that a peer region's Envoy can use it as a fallback
-- backend.
--
-- This must be an FQDN. Envoy Gateway only compiles multiple backendRefs into a single
-- Envoy cluster with priority levels when every Backend uses an fqdn endpoint; given an
-- IP it silently emits a weighted split across regions instead.
ALTER TABLE clusters
ADD COLUMN gateway_hostname TEXT;

COMMENT ON COLUMN clusters.gateway_hostname IS
  'Publicly resolvable FQDN of this cluster''s Envoy Gateway, e.g. us-east-1.deploy-app.com. Required for cross-region failover; NULL means this cluster cannot act as a failover peer.';
