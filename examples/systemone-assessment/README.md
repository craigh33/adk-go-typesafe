# Assessment callbacks

A Gemini-backed ADK agent assesses model input, model output, and proposed tool calls through TypeSafe. The application supplies a Noul question and a policy that blocks when the estimated probability of a credential being present is at least `0.5`. This threshold is illustrative; classification is not a substitute for access controls or deterministic secret detection.

```bash
export TYPESAFE_API_KEY='...'
export TYPESAFE_BASE_URL='https://api.typesafe.ai'
export GOOGLE_API_KEY='...'
export GEMINI_MODEL='your-supported-model'
make -C examples/systemone-assessment run PROMPT='"Draft a support ticket about a duplicate charge"'
```

`TYPESAFE_BASE_URL` is optional and defaults to `https://api.typesafe.ai`. Use the API root, without `/v1/systemone`.

The tool only returns a draft; it does not submit tickets. Use invented content when trying the example: assessments send the configured messages, outputs, and tool arguments to TypeSafe. API, policy, and state-storage errors stop execution.

The runner uses non-streaming responses. `AfterModel` rejects partial chunks before they can be forwarded. The latest assessment and policy decision are saved separately under `input_assessment`, `output_assessment`, and `tool_assessment` in session state. A tool call can also be blocked during output assessment before it reaches the tool callback.
