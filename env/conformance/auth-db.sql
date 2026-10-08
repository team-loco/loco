SELECT 'CREATE DATABASE loco_auth'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'loco_auth')\gexec

DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'supabase_auth_admin') THEN
        CREATE ROLE supabase_auth_admin LOGIN NOINHERIT CREATEROLE PASSWORD 'supabase_auth_admin';
    END IF;
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'anon') THEN
        CREATE ROLE anon NOLOGIN;
    END IF;
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'authenticated') THEN
        CREATE ROLE authenticated NOLOGIN;
    END IF;
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'service_role') THEN
        CREATE ROLE service_role NOLOGIN;
    END IF;
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'postgres') THEN
        CREATE ROLE postgres NOLOGIN;
    END IF;
END
$$;

ALTER DATABASE loco_auth OWNER TO supabase_auth_admin;
ALTER ROLE supabase_auth_admin SET search_path = auth;

\connect loco_auth
CREATE SCHEMA IF NOT EXISTS auth AUTHORIZATION supabase_auth_admin;
