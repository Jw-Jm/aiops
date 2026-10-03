# ADR-0022: Version the Node hardware causal alternative

Status: Implementation in progress

The Node Recipe v2 admits either the existing native runtime fault evidence or
an independently proven DIMM hardware fault upstream of the exact NotReady Node.
Node v1 remains readable and executable; the Registry requires signed publication
and activation before consuming v2. An alternative cannot be enabled by a
ranking score, a health alarm, proximity in the graph, or a recent change.

The hardware alternative requires current native Redfish health and
uncorrectable ECC evidence, component_of -> hosts -> scheduled_on provenance,
and one atomic native CPER log record containing a fatal NPD match and a DIMM
location matching exactly one upstream Redfish DeviceLocator. Separate log rows
are not concatenated. Missing location, ambiguous modules, absent source,
contradictory health or incomplete graph cannot confirm the cause. Unaggregated
CPER streams therefore remain unresolved rather than guessing the module.

The [Linux v6.12 CPER implementation](https://raw.githubusercontent.com/torvalds/linux/v6.12/drivers/firmware/efi/cper.c)
prints bank/device DIMM locations when firmware provides a valid DMI module
handle. This is protocol format evidence only; no Linux source is copied into the
runtime. Fatal matching reuses the already locked NPD rule. Physical CPER/BMC
validation is not claimed by a Fixture.

Concurrent proven runtime and hardware causes remain two candidates and produce
unresolved/partial with an explicit conflict. Evidence ranking remains the
locked ontology implementation. The RCA never discards a competing cause solely
because another candidate ranks higher. Each candidate carries real Evidence
references and fixed predicate/ontology provenance.

This remains within SP05 nonvirtual scope. VM diagnosis, investigator, command
execution and PyRCA runtime remain deferred or disabled as already recorded.

The original CPER format also distinguishes event severity from each section's
`Error N, type` severity. The located **memory section itself** must be fatal;
a fatal PCIe section plus corrected DIMM data is rejected. The NPD single-line
rules are replayed per line of one bounded atomic multiline record, preserving
record boundaries and native section order. The initial two-line protocol
example was corrected from the primary Linux format, not from reducer output.
`cper-memory-section-red.log` reproduces the unsafe inference and the stronger
section predicate is covered by `cper-fatal-memory-section-green.log`.

The native Node log template also fixes and verifies `_TRANSPORT="kernel"`.
The [original systemd journal field definition](https://raw.githubusercontent.com/systemd/systemd/main/man/systemd.journal-fields.xml)
distinguishes kernel input from stdout, journal and syslog input. Missing or
application transport cannot become an NPD finding or causal confirmation merely
because its text matches `BUG:`. The scoped source binding and its trusted kernel
collector are still required; a free caller field does not admit a source.
The Node golden input advances to v4 to make this origin explicit while preserving
all previous fixtures and the manually frozen diagnostic expectations. No test
output generated those expectations. Logs without these native origin fields
remain degraded/unusable as necessary kernel proof. Unknown CPER section markers
clear the previous section state, so they cannot inherit fatal memory severity.
