# PROMPTS.md

This document contains the prompts used during the implementation of this assignment.

---

# Prompt 1 – Software Design & Architecture

You are a Senior Staff Go Backend Engineer with extensive experience designing high-scale financial systems.

I have a backend engineering assignment for an interview.

The implementation language will be Go.

Do NOT generate any code.

First, analyze the problem and design a production-ready solution.

Requirements:

Build a RESTful Wallet Service supporting:

- Create Wallet
- Deposit
- Withdraw
- Transfer between wallets
- Transaction History

Business Rules:

- Transaction amount must be greater than zero.
- Withdrawal cannot exceed the wallet balance.
- Transfer to the same wallet is not allowed.
- Wallet balance must never become negative.
- Every financial operation must be recorded in transaction history.

Please provide:

1. High-level architecture
2. Project structure
3. Domain model
4. Database schema
5. REST API design
6. Validation strategy
7. Error handling strategy
8. Transaction management strategy
9. Concurrency considerations
10. Testing strategy
11. Scalability considerations
12. Design tradeoffs
13. Assumptions

Do not write any implementation code.

---

# Prompt 2 – Implementation

Based on the approved architecture, implement the project.

Requirements:

Language:
- Go

Database:
- PostgreSQL

Implementation requirements:

- Follow Clean Architecture.
- Follow idiomatic Go.
- Use context.Context properly.
- Keep the code modular and maintainable.
- Use dependency injection where appropriate.
- Keep handlers thin.
- Place business logic in the service layer.
- Place persistence logic in repositories.
- Use database transactions correctly.
- Ensure ACID guarantees for financial operations.
- Prevent race conditions.
- Prevent inconsistent balances.
- Validate all incoming requests.
- Return proper HTTP status codes.
- Generate production-quality code.

---

# Prompt 3 – Testing

Generate comprehensive tests.

Include:

- Unit tests
- Handler tests
- Service tests
- Repository tests where appropriate
- Table-driven tests
- Edge cases

Cover:

- Successful deposit
- Successful withdraw
- Successful transfer
- Insufficient balance
- Invalid amount
- Wallet not found
- Same-wallet transfer
- Transaction rollback
- Concurrent transfers

Aim for production-quality test coverage.

---

# Prompt 4 – Documentation

Generate README.md.

Include:

- Project overview
- Architecture
- Folder structure
- Installation
- Running locally
- Running tests
- API overview
- Assumptions
- Design decisions
- Current limitations
- Future improvements

---

# Prompt 5 – DESIGN.md

Generate DESIGN.md.

Answer:

1. Major design decisions.
2. How would the architecture evolve for several million daily transactions?
3. What is the biggest limitation or design risk?

Discuss realistic tradeoffs.

---

# Prompt 6 – Engineering Code Review

Act as a Principal Backend Engineer reviewing this submission.

Review the project for:

- Architecture
- Code quality
- REST API design
- Go best practices
- Database design
- Transaction correctness
- Concurrency issues
- Security concerns
- Performance bottlenecks
- Missing tests
- Documentation quality

For every issue:

- Explain the problem.
- Explain why it matters.
- Suggest improvements.
- Provide corrected code where appropriate.

---

# Prompt 7 – Requirements Compliance Review

Review the completed project against the original assignment.

Do not evaluate code style or architecture unless it affects compliance.

Instead, verify that every explicit and implicit requirement has been fully implemented.

For each requirement:

- State whether it is Fully Implemented, Partially Implemented, or Missing.
- Explain why.
- Identify any hidden assumptions.
- Point out any ambiguity in the original specification.

If any requirement is only partially satisfied or missing, propose the minimum set of production-quality changes required to achieve full compliance.

After listing the required changes, implement them while preserving the existing architecture, API design, and coding style.

Update any affected documentation and tests so the project remains internally consistent.

Finally, summarize whether the project can be considered a complete solution for the assignment after the changes are applied.

---

# Prompt 8 – API Documentation (Swagger / OpenAPI)

Generate OpenAPI (Swagger) documentation for the project.

Requirements:

- Generate production-quality API documentation.
- Use the standard Swagger/OpenAPI tooling for Go.
- Document every REST endpoint.
- Include request and response schemas.
- Document validation rules.
- Document all possible HTTP status codes.
- Include example requests and responses.
- Document error response format consistently.
- Keep the documentation synchronized with the current implementation.
- Expose Swagger UI through an endpoint suitable for local development.
- Update the README.md with instructions for generating and accessing the Swagger documentation.

Do not modify the API design unless required for documentation consistency.