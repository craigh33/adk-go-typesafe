# Context filter

The `plugins/contextfilter` ADK runner plugin uses Jev to select history for a Gemini-backed agent across a topic change and return. Saved conversation history remains available for later requests. Selection depends on Jev's judgments.

```bash
export TYPESAFE_API_KEY='...'
export TYPESAFE_BASE_URL='https://api.typesafe.ai'
export GOOGLE_API_KEY='...'
export GEMINI_MODEL='your-model-id'
make -C examples/context-filter run
```

`TYPESAFE_BASE_URL` is optional; the default is `https://api.typesafe.ai` without `/v1/systemone`. `TYPESAFE_MODEL` optionally selects a Jev version.

To inspect proposed removals while sending the complete context:

```bash
make -C examples/context-filter run ARGS=-observe
```

This short example sets `MinBytes: 1` and `KeepRecentTurns: 1` to demonstrate filtering. Library defaults skip histories below 8 KiB and protect two recent turns. Reports show original message ranges and Jev usage, not downstream tokens saved. Running the example calls both providers.
