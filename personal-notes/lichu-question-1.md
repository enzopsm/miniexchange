Hey Lichu, what's up, how's Carnival going?

I have a design question about the broker balance extension of the Engineering challenge. I'd like to get the input from the team on what approach I should follow.

The challenge mentions returning the balance of a broker, but doesn't specify how brokers come into existence or how they get their initial cash and stock holdings. I see a few options:

a) Seed via config file — a predefined list of brokers with their starting cash/holdings is loaded at startup, like an input file that sets the system's initial state. Only those brokers exist; any order referencing an unknown broker_id is rejected. Simple, but the set of brokers is closed.
b) Registration endpoint (POST /brokers) — brokers register at runtime with an initial cash deposit. Cleaner API story, but adds scope.
c) Implicit creation — brokers are auto-created on first order submission with a default balance.

Option a) keeps the focus on the matching engine, but option b) is more realistic for a production system. I'd gladly go with option b) if the team considers it's in scope.
