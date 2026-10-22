# Locate a park bench

Set your TypeSafe API key and API base URL, then run from the repository root:

```bash
export TYPESAFE_API_KEY='...'
export TYPESAFE_BASE_URL='https://api.typesafe.ai'
make -C examples/typesafe-box run
```

`TYPESAFE_BASE_URL` is optional and defaults to `https://api.typesafe.ai`. Use the API root, without `/v1/systemone`.

Sends the bundled [park photo](../images/empty-park.jpg), asks a Noul question about whether a bench is present, and a Box question to locate it. Prints the full response. Set `IMAGE=/absolute/path/to/photo.jpg` to use another PNG or JPEG. See the [image credits](../images/README.md) and [image requirements](../typesafe-images/README.md).

Box requires Jev 1.14, so this example pins `jev-1.14.0` and sets `Typesafe-Danger-Zone-Images: true` through `Options.Headers`. A live call uses your TypeSafe account.

Read the location with `response.Answers["bench"].(typesafe.BoxAnswer)`. Its `Box` contains `Top`, `Bottom`, `Left`, and `Right`, each between zero and one. Multiply horizontal edges by image width and vertical edges by image height to get pixel coordinates. `Confidence` is a `*float64` and may be nil when the model does not provide confidence.

A Box always returns coordinates, even when the target is absent. Use `bench_present` to decide whether to use the location; tune that decision for your images. See [TypeSafe's Box documentation](https://docs.typesafe.ai/primitives/box).
