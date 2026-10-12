# Native amd64 PostgreSQL distribution variant

The exact PostgreSQL 17.11 Debian 12 publisher index was selected independently. It differs from the historical admitted distribution in eight binary packages belonging to four source packages: pcre2, perl, OpenSSL and tzdata. Historical ARM locks remain unchanged.

The complete new source closure retains the original authenticated PostgreSQL and Debian source archive and adds the four exact updated source sets. Apt verified Debian archive signatures; all source file sizes and SHA-256 digests were independently checked against the retained Sources indexes. Original copyright notices of 136 unchanged native packages match byte for byte. Eight updated binary packages retain their new publisher notices under separate finite license scopes.

The resulting archive contains 638 verified inventory files. Native isolated TCP password authentication, `SELECT 1` and server version probes passed with a read-only root filesystem and network disabled. [Source variant proof](../evidence/amd64-deployment-20261009/postgresql-bookworm-source-closure-result.json) and [native SQL proof](../evidence/amd64-deployment-20261009/postgresql-bookworm-native-poc-result.json).

This admission concerns the exact native distribution and source/notice variant. Platform database roles, migrations on the installed release, RLS and business initialization remain separate acceptance work.
