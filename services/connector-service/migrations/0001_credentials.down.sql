-- Reverse of 0001_credentials.up.sql. pgcrypto is left in place — it is shared
-- with api's resources.connection_secret (still present until T-030's contract
-- leg drops it) and is part of the standard postgres:16 image.
DROP TABLE IF EXISTS connector_credentials;
