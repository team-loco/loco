INSERT INTO platform_domains (domain, is_active)
VALUES ('onloco.app', true), ('onloco.build', true)
ON CONFLICT DO NOTHING;
