# Assistant model Profile fixture

`model-profile.fixture.json` is shared by Go/Python contract tests and the isolated
loopback model smoke. It is `fixture_only`, never an admitted deployment Profile.

The weight, evaluation and admission digests (repeated 3, 1 and 2 respectively)
are synthetic test values. The license and capability fields are fixture
declarations, not verified license, weight identity or quality evidence. An
Ollama manifest digest is not a model weight digest and is not substituted here.

The real-model smoke calls the configured model through the locked Holmes
provider. Passing that smoke proves protocol/ledger connectivity, without
verifying weight provenance or turning this fixture into admission material.
