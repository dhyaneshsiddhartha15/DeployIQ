// Package model holds the domain types shared across transport, service and
// repository layers.
//
// Two deliberate constraints:
//
//   - No storage tags and no driver types. Identifiers are plain strings, not
//     MongoDB ObjectIDs. Phase 5.1 requires the db layer be "isolated so it
//     could be swapped later without touching business logic", which only holds
//     if the domain model carries no trace of the store. Converting between a
//     string ID and whatever the store uses is the repository's job.
//
//   - Types that only one package uses do not belong here. The CLI's internal
//     types (the detector's stack result, the parser's Dockerfile
//     representation, the rule engine's build context) live in their owning
//     packages under internal/, where they can change without a ripple.
//
// What is here is exactly what crosses a boundary: the two Phase 7.1
// collections, and the request/response shapes of the Phase 4.5 API contract.
package model
