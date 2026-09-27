# Activity stream response fixture

`latlng_response.json` is a synthetic, upstream-style `GET /activity/{id}/streams` response with non-athlete coordinates. It pins the public Intervals.icu behavior: for `latlng`, `data` is latitude and `data2` is longitude. The Intervals.icu maintainer clarified the second axis in the [latitude/longitude stream discussion](https://forum.intervals.icu/t/solved-possible-bug-on-latitude-longitude-stream/32420). The payload is constructed from that documented behavior; it is not copied from another server's source code or a private activity.
