# Image evaluation

Set your TypeSafe API key and API base URL, then run from the repository root:

```bash
export TYPESAFE_API_KEY='...'
export TYPESAFE_BASE_URL='https://api.typesafe.ai'
go run ./examples/typesafe-images examples/images/person-indoors.jpg
```

`TYPESAFE_BASE_URL` is optional and defaults to `https://api.typesafe.ai`. Use the API root, without `/v1/systemone`.

Sends a PNG or JPEG as a base64 JSON asset and asks Noul and Choice questions about it. The example sets `Typesafe-Danger-Zone-Images: true` through `Options.Headers` to enable experimental image support. Prints the answers, confidence, model, and token usage. A live call uses your TypeSafe account.

The bundled [person indoors](../images/person-indoors.jpg) and [empty park](../images/empty-park.jpg) are CC0 photographs from Wikimedia Commons; see the [image credits](../images/README.md). Pass either path, or your own image. Running `make run` from this example's directory uses the indoor sample; set `IMAGE=/path/to/photo.jpg` to override it.

Prepare an upright, still image with each side 256–1568 pixels and at least 78,400 total pixels. The file must be at most 16 MiB; the complete JSON request, including base64, must fit within 32 MiB. The client does not resize or rotate images. See [TypeSafe's image documentation](https://docs.typesafe.ai/images).
