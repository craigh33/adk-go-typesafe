# ADK image classification

Set your TypeSafe credentials, then run from the repository root:

```bash
export TYPESAFE_API_KEY='...'
export TYPESAFE_BASE_URL='https://api.typesafe.ai'
go run ./examples/systemone-images examples/images/empty-park.jpg
```

`TYPESAFE_BASE_URL` is optional and defaults to `https://api.typesafe.ai`. Use the API root without `/v1/systemone`.

Passes an image to an ADK runner with `genai.NewPartFromBytes`. The System One agent converts inline PNG/JPEG parts into TypeSafe assets automatically. The example sets `Typesafe-Danger-Zone-Images: true` through `Options.Headers`. Prints the assessment and stores it in session state. A live call uses your TypeSafe account; no generative model is needed. See the [image preparation limits](../typesafe-images/README.md).

The bundled [empty park](../images/empty-park.jpg) and [person indoors](../images/person-indoors.jpg) are CC0 photographs from Wikimedia Commons; see the [image credits](../images/README.md). Pass either path, or your own image. Running `make run` from this example's directory uses the park sample; set `IMAGE=/path/to/photo.jpg` to override it.

For images already stored as ADK artifacts, the System One function tool accepts `{"state": "Assess this photo", "artifacts": ["photo.png"]}`. Configure the runner's `ArtifactService`; the tool loads the named image through the current context's `Artifacts().Load`.
