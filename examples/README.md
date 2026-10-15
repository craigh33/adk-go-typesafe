# Examples

Set the TypeSafe environment variables before running an example:

```bash
export TYPESAFE_API_KEY='...'
export TYPESAFE_BASE_URL='https://api.typesafe.ai'
```

`TYPESAFE_API_KEY` is required. `TYPESAFE_BASE_URL` is optional and defaults to `https://api.typesafe.ai`; provide the API root without `/v1/systemone`. Each example passes these values to the client options.

- [typesafe-evaluate](typesafe-evaluate): evaluate structured state directly with Choice, Score, and Noul.
- [typesafe-images](typesafe-images): evaluate a PNG or JPEG with image assets.
- [typesafe-box](typesafe-box): locate a park bench with normalized image coordinates.
- [systemone-images](systemone-images): classify native ADK image parts.
- [systemone-tool](systemone-tool): attach an application-configured System One tool to an ADK agent.
- [bedrock-routing](bedrock-routing): route tickets to Bedrock-backed agents with confidence fallback.
- [systemone-assessment](systemone-assessment): assess model input, output, and tool calls.
- [context-filter](context-filter): select relevant conversation turns before each model call.

Each example has its own setup instructions, including any additional provider credentials. Run commands from the repository root unless the example says otherwise.
