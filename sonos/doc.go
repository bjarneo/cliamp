// Package sonos controls Sonos speakers over SSDP and UPnP SOAP.
//
// The SOAP action and Spotify ShareLink DIDL conventions are translated from
// SoCo (https://github.com/SoCo/SoCo), Copyright (c) Rahim Sonawalla and
// individual contributors, MIT licensed. The source translation is pinned to
// SoCo commit 0aa432ab428f7cf30f16cb51c3251e268d9e490a, inspected on
// 2026-10-14: soco/services.py, soco/core.py, soco/zonegroupstate.py and
// soco/plugins/sharelink.py. The earlier helper had no revision pin. This is a
// targeted implementation, not a SoCo port.
package sonos
