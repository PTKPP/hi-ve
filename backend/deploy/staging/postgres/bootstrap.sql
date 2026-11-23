-- Run as the cluster administrator for a NEW, dedicated staging database.
-- Set the authorized credentials through the operator's secret channel.
\set ON_ERROR_STOP on
CREATE ROLE hive_migrator LOGIN;
CREATE ROLE hive_app LOGIN;
CREATE ROLE hive_backup LOGIN REPLICATION;
CREATE DATABASE hive_staging OWNER hive_migrator;
REVOKE ALL ON DATABASE hive_staging FROM PUBLIC;
GRANT CONNECT ON DATABASE hive_staging TO hive_app;
