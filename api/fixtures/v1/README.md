# Version 1 fixtures

All identities, messages, grants and vouchers here are synthetic test data. No production credentials or private messages are included.

| Fixture | Purpose |
| --- | --- |
| `routing-header.json` | Literal RFC 8785 subset canonical routing AAD. |
| `message.json` | Exact message wire shape; control-looking body remains data. |
| `receipt.json` | Ambiguous submission remains `in_doubt`, not delivery. |
| `packet.json` | Schema round trip only; zero encapsulation/ciphertext cannot pass admission. |
| `crypto-rfc9180.json` | Published exact-suite RFC 9180 Base ciphertext and public test key material. |
| `crypto-rfc8037.json` | Published Ed25519 JWS bytes and public test key material. |
| `invite-packet-synthetic.json` | Synthetic invitation packet schema round trip, not a valid encrypted descriptor. |
| `invite-packet-invalid.json` | An unknown authority field must be rejected. |

Invitation packet fixtures record synthetic wire shapes. Valid descriptor/claim chains are generated in memory by their acceptance tests. Complete secret invitation URLs are not golden artifacts.

RFC vector provenance: [RFC 9180 Appendix A.2.1](https://www.rfc-editor.org/rfc/rfc9180.html#appendix-A.2.1), [RFC 8037 Appendix A](https://www.rfc-editor.org/rfc/rfc8037.html#appendix-A). RFC material retains its original ownership and [IETF Trust legal provisions](https://trustee.ietf.org/license-info/). These fixtures preserve standardized test facts and do not import a cryptographic implementation.

Reproduction commands and checks are in [M1a fix evidence](../../../docs/core-m1a-evidence.md). The Go tests compare the stored bytes or authenticated semantics; they do not silently regenerate golden expectations.
