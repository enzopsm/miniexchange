# B3, NYSE and CLOB

Both B3 and NYSE operate what's called a Central Limit Order Book (CLOB). This is the data structure at the heart of your challenge:

- **Order Book:** For each stock symbol, there are two sorted lists:
  - **Bids (buy orders):** sorted by price descending (highest price first), then by time ascending (oldest first at same price)
  - **Asks (sell orders):** sorted by price ascending (lowest price first), then by time ascending
- **Matching:** When a new order arrives, the engine checks if it can match against the opposite side of the book. A match happens when the best bid ≥ best ask.
- **Partial fills:** If a buy order for 1000 shares matches a sell order for 500, the sell is fully filled and the buy remains in the book with 500 remaining.
- **Price-time priority (FIFO):** This is exactly what the challenge describes — "whoever submitted their order first" gets priority among equal prices.

So the challenge is essentially asking you to implement a simplified CLOB matching engine with an API layer on top. The references to B3 and NYSE are just saying "this is the real-world system you're modeling in miniature."

For your implementation, the key data structure insight is: the order book per symbol is essentially two priority queues (min-heap for asks, max-heap for bids), where the comparison is (price, then timestamp). When a new order comes in, you try to match it against the top of the opposite side until no more matches are possible, then insert the remainder into the book.

# Programming Language

## What you should use for this challenge

Here's the thing — you're not building a real exchange. You're building a take-home project that will be presented and evaluated. The priorities are different:

What they're evaluating:
- Clean architecture and code quality
- Correct matching logic
- Good API design
- Containerization (OCI-compliant)
- How you communicate and make decisions

What they're NOT evaluating:
- Nanosecond latency
- Handling millions of orders per second

### My recommendation: Go

Reasons:

- Excellent for building HTTP/gRPC APIs with minimal boilerplate — net/http or a lightweight framework gets you there fast
- Concurrency is trivial with goroutines if you want to process orders asynchronously or handle webhooks (one of the extensions)
- Compiles to a single static binary — makes your Dockerfile dead simple (FROM scratch or FROM alpine + copy binary)
- Strong standard library means fewer dependencies
- Readable code — the people reviewing your solution will understand it quickly
- You get "good enough" performance without any effort

# OCI Compliant

Provide a Dockerfile (and ideally a docker-compose.yml if you have multiple services like API + DB + frontend). That's it. Don't overthink this — a well-written Dockerfile already satisfies the OCI requirement.
