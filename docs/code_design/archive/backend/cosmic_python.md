- hexagonal pattern for 3rd party integration (cosmic python)
    
    > A working reference for structuring a Python service using Domain-Driven Design + Ports & Adapters (Hexagonal Architecture), with Stripe, Celery, Clerk, and OpenAI as third-party integrations, served via FastAPI.
    > 
    
    Use **ArchUnitPython** or **PyTestArch** to validate code structure, layer dependencies, and naming conventions as automated tests
    
    ---
    
    ## 📁 Folder Structure
    
    ```
    myapp/
    ├── domain/
    │   ├── order.py
    │   ├── order_status.py
    │   ├── authenticated_user.py
    │   └── payment_errors.py
    ├── service_layer/
    │   ├── auth.py
    │   ├── purchase.py
    │   └── assistant.py
    ├── integrations/
    │   ├── errors.py
    │   ├── db/
    │   │   ├── model/
    │   │   │   └── order.py
    │   │   ├── repository.py
    │   │   └── unit_of_work.py
    │   ├── payment_gateway.py
    │   ├── notifications.py
    │   ├── auth/
    │   │   └── clerk.py
    │   └── ai/
    │       └── openai_client.py
    ├── entrypoints/
    │   ├── api/
    │   │   ├── app.py
    │   │   ├── deps.py
    │   │   ├── middleware/
    │   │   │   └── auth_middleware.py
    │   │   └── routes/
    │   │       ├── orders.py
    │   │       └── assistant.py
    │   └── worker/
    │       └── celery_app.py
    ├── bootstrap.py
    └── main.py
    ```
    
    ---
    
    ## 🧠 The One Rule Everything Follows
    
    > `domain/` and `service_layer/` may only import from `domain/`, `service_layer/`, and **ports/abstract classes + plain data types** inside `integrations/`.
    `integrations/` may import from `domain/` freely.
    Only `bootstrap.py` and `entrypoints/` are allowed to know about *concrete* adapters (Stripe, Clerk, OpenAI, SQLAlchemy) all at once.
    > 
    
    If you ever see `import stripe`, `import openai`, `import jwt`, or `from sqlalchemy import ...` inside `domain/` or `service_layer/` — that's the architecture breaking.
    
    ---
    
    ## 🟦 domain/ — pure business logic, zero third-party imports
    
    ### `domain/order_status.py`
    
    ```python
    from enum import Enum
    
    class OrderStatus(Enum):
        PENDING = "pending"
        PAID = "paid"
    ```
    
    ### `domain/order.py`
    
    ```python
    from dataclasses import dataclass
    from domain.order_status import OrderStatus
    
    @dataclass
    class Order:
        id: str
        amount_cents: int
        customer_reference: str
        status: OrderStatus = OrderStatus.PENDING
    
        def mark_as_paid(self):
            self.status = OrderStatus.PAID
    ```
    
    ### `domain/authenticated_user.py`
    
    ```python
    from dataclasses import dataclass
    
    @dataclass
    class AuthenticatedUser:
        user_id: str
        email: str | None = None
    ```
    
    > Lives in `domain/` (not inside the Clerk adapter) because "a user who has been authenticated" is a business concept, not a Clerk-specific one. Any auth provider (Clerk, Auth0, homegrown) would need to produce this same shape.
    > 
    
    ### `domain/payment_errors.py`
    
    ```python
    class PaymentError(Exception):
        """Base exception for anything that can go wrong charging a customer."""
    
    class PaymentDeclinedError(PaymentError):
        """The customer's payment method was declined. A normal business outcome,
        not a bug — happens regardless of which payment provider is used."""
    ```
    
    > `PaymentDeclinedError` is a **domain** error because "payment was declined" is business-meaningful with or without Stripe. Compare to `ProviderUnavailableError` below, which is purely operational.
    > 
    
    ---
    
    ## 🟩 integrations/ — third-party glue (ports + adapters, one file per integration)
    
    Adapter pattern used in all files here
    
    ### `integrations/errors.py` — shared, provider-agnostic infrastructure error
    
    ```python
    class ProviderUnavailableError(Exception):
        """
        Raised when ANY third-party service (Stripe, OpenAI, Clerk, Twilio, etc.)
        is unreachable, times out, or returns a server-side error unrelated to
        the specific request being made.
    
        Intentionally generic and provider-agnostic — the caller only needs to
        know "an external dependency failed," not which one or why, since the
        handling (retry, alert, return a 503) is typically the same regardless
        of which provider is behind it.
        """
    
        def __init__(self, provider: str, message: str):
            self.provider = provider
            super().__init__(f"[{provider}] {message}")
    ```
    
    > Used by **every** adapter below instead of each inventing its own "XProviderDownError" class.
    > 
    
    ---
    
    ### `integrations/db/model/order.py` — ORM mapping (declarative, separate from domain `Order`)
    
    ```python
    from sqlalchemy import Column, String, Integer, Enum as SAEnum
    from sqlalchemy.orm import declarative_base
    from domain.order_status import OrderStatus
    
    Base = declarative_base()
    
    class OrderModel(Base):
        __tablename__ = "orders"
        id = Column(String, primary_key=True)
        amount_cents = Column(Integer, nullable=False)
        customer_reference = Column(String, nullable=False)
        status = Column(SAEnum(OrderStatus), nullable=False)
    ```
    
    ### `integrations/db/repository.py` — port + adapter + conversion functions
    
    ```python
    from abc import ABC, abstractmethod
    from typing import Optional
    from sqlalchemy.orm import Session
    from sqlalchemy.exc import IntegrityError
    from domain.order import Order
    from integrations.db.model.order import OrderModel
    
    # ---- conversion functions ----
    
    def to_domain(model: OrderModel) -> Order:
        """Convert a database row (OrderModel) into a domain object (Order)."""
        return Order(
            id=model.id,
            amount_cents=model.amount_cents,
            customer_reference=model.customer_reference,
            status=model.status,
        )
    
    def to_model(order: Order) -> OrderModel:
        """Convert a domain object (Order) into a database row (OrderModel)."""
        return OrderModel(
            id=order.id,
            amount_cents=order.amount_cents,
            customer_reference=order.customer_reference,
            status=order.status,
        )
    
    # ---- port-specific error ----
    
    class DuplicateOrderError(Exception):
        """
        Raised when attempting to add() an order whose id already exists.
    
        Lives alongside the port (not in domain/) because "duplicate" here is
        specifically a repository/storage-layer concern — the same reasoning
        as AuthenticationError living in integrations/auth/clerk.py rather than
        domain/: it's specific to how this port behaves, not a business concept
        that would exist independent of having a repository at all.
        """
    
        def __init__(self, order_id: str):
            self.order_id = order_id
            super().__init__(f"Order with id '{order_id}' already exists")
    
    # ---- port ----
    
    class AbstractOrderRepository(ABC):
        @abstractmethod
        def get(self, order_id: str) -> Optional[Order]:
            raise NotImplementedError
    
        @abstractmethod
        def add(self, order: Order) -> None:
            """
            Raises:
                DuplicateOrderError: an order with this id has already been added.
            """
            raise NotImplementedError
    
    # ---- adapter ----
    
    class SqlAlchemyOrderRepository(AbstractOrderRepository):
        def __init__(self, session: Session):
            self.session = session
    
        def get(self, order_id: str) -> Optional[Order]:
            row = self.session.query(OrderModel).filter_by(id=order_id).first()
            if row is None:
                return None
            return to_domain(row)
    
        def add(self, order: Order) -> None:
            try:
                self.session.add(to_model(order))
                self.session.flush()
            except IntegrityError as e:
                self.session.rollback()
                raise DuplicateOrderError(order.id) from e
    ```
    
    ---
    
    ### `integrations/payment_gateway.py` — port + Stripe adapter
    
    ```python
    from abc import ABC, abstractmethod
    from dataclasses import dataclass
    import stripe
    
    from domain.payment_errors import PaymentDeclinedError
    from integrations.errors import ProviderUnavailableError
    
    @dataclass
    class PaymentResult:
        success: bool
        transaction_id: str | None = None
    
    class AbstractPaymentGateway(ABC):
        """
        A 'payment gateway' is the thing that actually moves money — it takes a
        card/bank token and an amount, and talks to a payment processor (Stripe,
        PayPal, Square, etc.) to charge the customer. Your backend never talks
        to Visa or a bank directly; the gateway handles compliance, fraud
        detection, and the actual money movement.
    
        This class defines WHAT a payment gateway must do, without saying WHICH
        provider does it. Business logic only ever talks to this interface.
        """
    
        @abstractmethod
        def charge(self, amount_cents: int, customer_reference: str) -> PaymentResult:
            """
            Raises:
                PaymentDeclinedError: the customer's payment method was declined.
                ProviderUnavailableError: Stripe's API was unreachable or errored.
            """
            raise NotImplementedError
    
    class StripePaymentGateway(AbstractPaymentGateway):
        """
        customer_reference here is a Stripe token/payment-method-id representing
        the customer's card, collected on the frontend via Stripe.js/Elements —
        never a raw card number.
        """
    
        def __init__(self, api_key: str):
            stripe.api_key = api_key
    
        def charge(self, amount_cents: int, customer_reference: str) -> PaymentResult:
            try:
                charge = stripe.Charge.create(
                    amount=amount_cents,        # smallest currency unit (cents)
                    currency="usd",
                    source=customer_reference,
                )
                return PaymentResult(success=True, transaction_id=charge.id)
    
            except stripe.error.CardError as e:
                raise PaymentDeclinedError(str(e)) from e
    
            except (stripe.error.APIConnectionError, stripe.error.APIError) as e:
                raise ProviderUnavailableError("stripe", str(e)) from e
    ```
    
    ---
    
    ### `integrations/notifications.py` — port + Celery adapter
    
    ```python
    from abc import ABC, abstractmethod
    
    class AbstractNotifications(ABC):
        @abstractmethod
        def send_receipt_email(self, order_id: str) -> None:
            raise NotImplementedError
    
    class CeleryNotifications(AbstractNotifications):
        def send_receipt_email(self, order_id: str) -> None:
            from entrypoints.worker.celery_app import send_receipt_email_task
            send_receipt_email_task.delay(order_id)
    ```
    
    ---
    
    ### `integrations/auth/clerk.py` — port + Clerk adapter
    
    ```python
    from abc import ABC, abstractmethod
    from functools import lru_cache
    import jwt
    import requests
    
    from domain.authenticated_user import AuthenticatedUser
    from integrations.errors import ProviderUnavailableError
    
    class AuthenticationError(Exception):
        """The token itself was invalid, expired, or malformed."""
    
    class AbstractAuthenticator(ABC):
        @abstractmethod
        def verify_token(self, token: str) -> AuthenticatedUser:
            """
            Raises:
                AuthenticationError: token is invalid, expired, or malformed.
                ProviderUnavailableError: Clerk's JWKS endpoint was unreachable.
            """
            raise NotImplementedError
    
    class ClerkAuthenticator(AbstractAuthenticator):
        def __init__(self, clerk_jwks_url: str, clerk_issuer: str, audience: str | None = None):
            self._jwks_url = clerk_jwks_url
            self._issuer = clerk_issuer
            self._audience = audience
    
        @lru_cache(maxsize=1)
        def _get_signing_key(self, kid: str):
            jwks_client = jwt.PyJWKClient(self._jwks_url)
            return jwks_client.get_signing_key(kid)
    
        def verify_token(self, token: str) -> AuthenticatedUser:
            try:
                unverified_header = jwt.get_unverified_header(token)
                signing_key = self._get_signing_key(unverified_header["kid"])
    
                payload = jwt.decode(
                    token,
                    signing_key.key,
                    algorithms=["RS256"],
                    issuer=self._issuer,
                    audience=self._audience,
                    options={"verify_aud": self._audience is not None},
                )
                return AuthenticatedUser(user_id=payload["sub"], email=payload.get("email"))
    
            except jwt.PyJWTError as e:
                raise AuthenticationError(str(e)) from e
    
            except requests.exceptions.RequestException as e:
                raise ProviderUnavailableError("clerk", str(e)) from e
    ```
    
    ---
    
    ### `integrations/ai/openai_client.py` — port + OpenAI adapter
    
    ```python
    from abc import ABC, abstractmethod
    from openai import OpenAI, APIConnectionError, APIError
    
    from integrations.errors import ProviderUnavailableError
    
    class AbstractAssistant(ABC):
        """
        Represents 'something that can generate a text response given a prompt.'
        Doesn't say which AI provider does it — could be OpenAI, Anthropic, a
        local model, etc. Business logic only depends on this shape.
        """
    
        @abstractmethod
        def generate_reply(self, prompt: str) -> str:
            """
            Raises:
                ProviderUnavailableError: OpenAI's API was unreachable or errored.
            """
            raise NotImplementedError
    
    class OpenAIAssistant(AbstractAssistant):
        def __init__(self, api_key: str, model: str = "gpt-4o-mini"):
            self._client = OpenAI(api_key=api_key)
            self._model = model
    
        def generate_reply(self, prompt: str) -> str:
            try:
                response = self._client.chat.completions.create(
                    model=self._model,
                    messages=[{"role": "user", "content": prompt}],
                )
                return response.choices[0].message.content
    
            except (APIConnectionError, APIError) as e:
                raise ProviderUnavailableError("openai", str(e)) from e
    ```
    
    ### `integrations/db/unit_of_work.py` — port + adapter

    ```python
    from abc import ABC, abstractmethod
    from sqlalchemy.orm import sessionmaker

    from integrations.db.repository import (
        AbstractOrderRepository,
        SqlAlchemyOrderRepository,
    )

    class AbstractUnitOfWork(ABC):
        """
        A Unit of Work is a single atomic chunk of work: everything inside the
        `with` block either lands together or not at all. It also owns the
        repositories for the duration of that block, so the service layer never
        has to construct or wire a session itself.

        Exiting the block WITHOUT calling commit() rolls back. That default is
        deliberate: forgetting to commit loses work loudly, rather than
        half-persisting it silently.
        """

        orders: AbstractOrderRepository   # type annotation only — no object created
                                          # here; the concrete subclass assigns the
                                          # real repository in __enter__

        def __enter__(self) -> "AbstractUnitOfWork":
            return self

        def __exit__(self, *args):
            self.rollback()               # no-op if commit() already ran

        @abstractmethod
        def commit(self):
            raise NotImplementedError

        @abstractmethod
        def rollback(self):
            raise NotImplementedError

    class SqlAlchemyUnitOfWork(AbstractUnitOfWork):
        def __init__(self, session_factory: sessionmaker):
            self._session_factory = session_factory

        def __enter__(self):
            self._session = self._session_factory()
            self.orders = SqlAlchemyOrderRepository(self._session)
            return super().__enter__()

        def __exit__(self, *args):
            super().__exit__(*args)
            self._session.close()

        def commit(self):
            self._session.commit()

        def rollback(self):
            self._session.rollback()
    ```
    
    > Note: this file *does* import a concrete adapter (`SqlAlchemyOrderRepository`) — the accepted exception, since a Unit of Work's whole job is gluing a live session to a concrete repository implementation. It now lives alongside the rest of the DB integration code in `integrations/db/`, rather than in `service_layer/`.
    > 
    
    ---
    
    ## 🟨 service_layer/ — orchestration, only depends on ports + domain
    
    ### `service_layer/auth.py`
    
    ```python
    from domain.authenticated_user import AuthenticatedUser
    from integrations.auth.clerk import AbstractAuthenticator
    
    def authenticate(token: str, authenticator: AbstractAuthenticator) -> AuthenticatedUser:
        return authenticator.verify_token(token)
    ```
    
    ### `service_layer/purchase.py`
    
    ```python
    from domain.order import Order
    from domain.payment_errors import PaymentDeclinedError
    from integrations.db.unit_of_work import AbstractUnitOfWork
    from integrations.notifications import AbstractNotifications
    from integrations.payment_gateway import AbstractPaymentGateway
    from integrations.errors import ProviderUnavailableError
    
    def place_order(
        order_id: str,
        amount_cents: int,
        customer_reference: str,
        uow: AbstractUnitOfWork,
        payment_gateway: AbstractPaymentGateway,
        notifications: AbstractNotifications,
    ):
        with uow:
            order = Order(id=order_id, amount_cents=amount_cents, customer_reference=customer_reference)
    
            try:
                payment_gateway.charge(amount_cents, customer_reference)
            except (PaymentDeclinedError, ProviderUnavailableError):
                raise  # let the route layer translate this into the right HTTP status
    
            order.mark_as_paid()
            uow.orders.add(order)
            uow.commit()
            notifications.send_receipt_email(order.id)
    ```
    
    > **Book alignment note:** this signature already follows Cosmic Python's
    "express the service layer in primitives, not domain objects" guidance —
    `order_id: str`, `amount_cents: int`, `customer_reference: str` — rather
    than taking an `Order` object as a parameter. No change needed here; it's
    the reason the service-layer tests below can stay decoupled from `Order`'s
    internal shape (see the coupling discussion in section 2).
    > 
    
    ### `service_layer/assistant.py`
    
    ```python
    from integrations.ai.openai_client import AbstractAssistant
    
    def ask_assistant(prompt: str, assistant: AbstractAssistant) -> str:
        return assistant.generate_reply(prompt)
    ```
    
    ---
    
    ## 🟧 entrypoints/api/ — everything FastAPI-specific lives here
    
    > Isolated under `api/` (a sibling to a future `entrypoints/worker/`) so that swapping FastAPI for another framework only ever touches this one folder — never `domain/`, `service_layer/`, or `integrations/`.
    > 
    
    ### `entrypoints/api/deps.py`
    
    ```python
    from fastapi import Request
    
    def get_deps(request: Request) -> dict:
        """
        Returns the shared dependency container built once at startup by bootstrap().
    
        WHY THIS FUNCTION EXISTS:
    
        Without it, every route file would reach into `request.app.state.deps`
        directly, e.g.:
    
            # entrypoints/api/routes/orders.py
            @router.post("/orders")
            def create_order(request: Request, ...):
                deps = request.app.state.deps          # duplicated in every route
                uow = deps["uow"]
                payment_gateway = deps["payment_gateway"]
                ...
    
            # entrypoints/api/routes/assistant.py
            @router.post("/assistant")
            def ask(request: Request, ...):
                deps = request.app.state.deps          # duplicated again here
                assistant = deps["assistant"]
                ...
        """
        return request.app.state.deps
    ```
    
    ### `entrypoints/api/middleware/auth_middleware.py`
    
    ```python
    from starlette.middleware.base import BaseHTTPMiddleware
    from starlette.requests import Request
    from starlette.responses import JSONResponse
    
    from service_layer.auth import authenticate
    from integrations.auth.clerk import AuthenticationError
    from integrations.errors import ProviderUnavailableError
    
    PUBLIC_PATHS = {"/health", "/docs", "/openapi.json"}
    
    class AuthMiddleware(BaseHTTPMiddleware):
        async def dispatch(self, request: Request, call_next):
            if request.url.path in PUBLIC_PATHS:
                return await call_next(request)
    
            auth_header = request.headers.get("Authorization", "")
            if not auth_header.startswith("Bearer "):
                return JSONResponse({"error": "Missing bearer token"}, status_code=401)
    
            token = auth_header.removeprefix("Bearer ")
            authenticator = request.app.state.deps["authenticator"]
    
            try:
                user = authenticate(token, authenticator)
            except AuthenticationError as e:
                return JSONResponse({"error": str(e)}, status_code=401)
            except ProviderUnavailableError as e:
                return JSONResponse({"error": f"{e.provider} is currently unavailable"}, status_code=503)
    
            request.state.user = user
            return await call_next(request)
    ```
    
    ### `entrypoints/api/routes/orders.py`
    
    ```python
    from fastapi import APIRouter, Depends, HTTPException, Request
    from entrypoints.api.deps import get_deps
    from service_layer.purchase import place_order
    from domain.payment_errors import PaymentDeclinedError
    from integrations.errors import ProviderUnavailableError
    
    router = APIRouter()
    
    @router.post("/orders")
    def create_order(
        order_id: str,
        amount_cents: int,
        customer_reference: str,
        request: Request,
        deps: dict = Depends(get_deps),
    ):
        user = request.state.user  # attached by AuthMiddleware
    
        try:
            place_order(
                order_id=order_id,
                amount_cents=amount_cents,
                customer_reference=customer_reference,
                uow=deps["uow"],
                payment_gateway=deps["payment_gateway"],
                notifications=deps["notifications"],
            )
        except PaymentDeclinedError as e:
            raise HTTPException(status_code=402, detail=str(e))
        except ProviderUnavailableError as e:
            raise HTTPException(status_code=503, detail=f"{e.provider} is currently unavailable")
    
        return {"status": "ok", "created_by": user.user_id}
    ```
    
    ### `entrypoints/api/routes/assistant.py`
    
    ```python
    from fastapi import APIRouter, Depends, HTTPException
    from entrypoints.api.deps import get_deps
    from service_layer.assistant import ask_assistant
    from integrations.errors import ProviderUnavailableError
    
    router = APIRouter()
    
    @router.post("/assistant")
    def ask(prompt: str, deps: dict = Depends(get_deps)):
        try:
            reply = ask_assistant(prompt, deps["assistant"])
        except ProviderUnavailableError as e:
            raise HTTPException(status_code=503, detail=f"{e.provider} is currently unavailable")
    
        return {"reply": reply}
    ```
    
    ### `entrypoints/api/app.py`
    
    ```python
    from fastapi import FastAPI
    from bootstrap import bootstrap
    from entrypoints.api.middleware.auth_middleware import AuthMiddleware
    from entrypoints.api.routes import orders, assistant
    
    def create_app() -> FastAPI:
        app = FastAPI()
        app.state.deps = bootstrap()
        app.add_middleware(AuthMiddleware)
    
        app.include_router(orders.router)
        app.include_router(assistant.router)
    
        return app
    ```
    
    ---
    
    ## 🟥 Composition Root
    
    ### `bootstrap.py`
    
    ```python
    import os
    from sqlalchemy import create_engine
    from sqlalchemy.orm import sessionmaker
    
    from integrations.db.model.order import Base
    from integrations.payment_gateway import StripePaymentGateway
    from integrations.notifications import CeleryNotifications
    from integrations.auth.clerk import ClerkAuthenticator
    from integrations.ai.openai_client import OpenAIAssistant
    from integrations.db.unit_of_work import SqlAlchemyUnitOfWork
    
    def bootstrap() -> dict:
        engine = create_engine(os.environ["DATABASE_URL"])
        Base.metadata.create_all(engine)
        session_factory = sessionmaker(bind=engine)
    
        return {
            "uow": SqlAlchemyUnitOfWork(session_factory),
            "payment_gateway": StripePaymentGateway(api_key=os.environ["STRIPE_API_KEY"]),
            "notifications": CeleryNotifications(),
            "authenticator": ClerkAuthenticator(
                clerk_jwks_url=os.environ["CLERK_JWKS_URL"],
                clerk_issuer=os.environ["CLERK_ISSUER"],
            ),
            "assistant": OpenAIAssistant(api_key=os.environ["OPENAI_API_KEY"]),
        }
    ```
    
    ### `main.py`
    
    ```python
    from entrypoints.api.app import create_app
    
    app = create_app()
    ```
    
    Run with:
    
    ```bash
    uvicorn main:app --reload
    ```
    
    > `bootstrap()` runs **exactly once per process**, here in `main.py`. A future `entrypoints/worker/celery_app.py` would call `bootstrap()` once at worker startup too — never per-request or per-task.
    > 
    
    ---
    
    ## 📖 Reference: Error Handling Pattern for Third-Party Services
    
    **Rule:** Catch the third-party's exception inside the adapter. Never let it propagate past `integrations/`. Raise your own exception instead — always with `raise ... from e` to preserve the original traceback for debugging.
    
    **Where the new exception type lives — apply this test:**
    
    > "Would this error concept still make sense if we removed the third-party entirely?"
    > 
    
    | Question | Answer | Lives in |
    | --- | --- | --- |
    | "Payment was declined" | Still meaningful without Stripe specifically | `domain/payment_errors.py` |
    | "Provider's API was unreachable/erroring" | Only exists because of the third-party dependency; identical shape across all providers | `integrations/errors.py` (shared, generic) |
    | "Token was invalid/expired" | Specific to *how* auth tokens work, not really domain, but also not generic across providers | `integrations/auth/clerk.py` (specific to that port) |
    
    **Why `ProviderUnavailableError` is shared across all integrations, not one-per-provider:**
    
    - Structurally identical everywhere (network unreachable, 5xx, timeout)
    - Lets route/middleware code catch **one** exception type regardless of which provider is down
    - Carries a `provider: str` field so you don't lose *which* service failed, without needing a new exception *class* per provider
    
    **What NOT to over-generalize:** business-specific failure outcomes (e.g. `PaymentDeclinedError` vs a Twilio "invalid phone number" error) must stay distinct, since they need different handling — a declined card is a 402 ("update payment method"), a provider outage is a 503 ("try again later"). Only merge failure types that are genuinely operationally identical.
    
    ---
    
    ## Testing
    
    **Each layer owns testing the branching logic that originates at that layer.** Domain tests exhaustively cover every combination of the domain object's own business rules/invariants. Service-layer tests exhaustively cover every combination of **the service layer's own branches** (which exceptions it catches, what it does on success vs. failure, what order it calls things in) — but treat the domain object as a trusted black box, using it in only the minimal way needed to drive the service layer's own branches, not to re-explore the domain object's internal state space.
    
    - Stubs, Fakes, and Mocks — definitions
        
        **Stub** — returns pre-programmed (canned) answers when called. No real logic inside, just fixed output. You use it to feed your code a controlled input; you don't check *how* it was called, only that your code produced the right result *given* that input.
        
        ```python
        class StubPaymentGateway:
            def charge(self, amount_cents, customer_reference):
                return PaymentResult(success=True, transaction_id="stub-txn")  # always this, no matter what's passed in
        ```
        
        **Fake** — has a real, working implementation, just simplified compared to production (e.g., an in-memory dict instead of a real database). It actually *behaves* correctly for the operations it supports — you can add something and get it back out, because there's genuine logic doing that, not a hardcoded answer.
        
        ```python
        class FakeOrderRepository:
            def __init__(self):
                self._orders = {}
            def add(self, order):
                self._orders[order.id] = order      # real logic
            def get(self, order_id):
                return self._orders.get(order_id)   # real logic
        ```
        
        **Mock** — a double you set expectations on, then verify afterward that specific interactions happened (was this called, with what arguments, how many times). You assert on *how it was used*, not on any resulting state, because often there's no state to check.
        
        ```python
        mock_send_mail = Mock()
        mock_send_mail("stock@made.com", "Out of stock")
        mock_send_mail.assert_called_once_with("stock@made.com", "Out of stock")  # checking the CALL, not any state
        ```
        
        ## Mapping this onto what we've actually built
        
        | Object | Type | Why |
        | --- | --- | --- |
        | `FakeOrderRepository` | **Fake** | Real dict-based add/get logic — genuinely stores and retrieves, not canned |
        | `FakeUnitOfWork` | **Fake** | Tracks real `committed` state via actual logic |
        | `FakePaymentGateway` | **Stub** (with call-recording bolted on) | Returns a pre-set canned result (`should_succeed`); no real charge computation happens |
        | `FakeNotifications` | **Stub** (with call-recording bolted on) | Just appends to a list — no real "sending" logic, just recording what was passed in |
        | `mock.patch("...email.send")` | **Mock** | We verify the interaction (`assert_called_once_with(...)`) — there's no state to check, sending an email has no inspectable result, so a mock is the only sensible choice |
        
        ## The one distinction that decides which to use
        
        - **Fake** → your code needs to depend on genuinely correct *behavior* (store something, then retrieve it correctly later).
        - **Stub** → your code just needs a fixed *input* to react to, and you check the *outcome* of your own code, not the double itself.
        - **Mock** → the *only* thing worth checking is that a specific call happened — typically because the real operation is a side effect with no resulting state you could otherwise inspect (sending an email, calling a third-party API).
        
        This is also the precise reason `Mock()` was wrong for the repository earlier in our conversation: a repository needs to exhibit real state-holding *behavior* (add then get should agree), which only a **Fake** can do — a Mock configured with `.get.return_value = X` just returns `X` regardless of what was actually "added," so it can't catch bugs in how your code uses add/get together.
        
    - Criticism of mocks
        
        ## The actual criticism of mocks, stated precisely
        
        The problem isn't "mocks are bad" — it's that **mocks test implementation details instead of behavior**, which makes tests brittle. A mock passes or fails based on *how* your code called something, not on *whether the result was correct*. This means:
        
        **Refactoring breaks tests that shouldn't break.** If you reorder two independent lines inside `place_order`, or change *how* you call a dependency while producing the exact same correct outcome, a mock-heavy test can fail — even though your program's actual behavior didn't change at all.
        
        ```python
        # If your test asserts EXACTLY this call sequence:
        mock_repo.add.assert_called_once_with(order)
        mock_uow.commit.assert_called_once()
        
        # ...and someone refactors to call commit() slightly differently internally,
        # or restructures without changing observable behavior — the test breaks
        # for no real reason. This is "coupling to implementation," not "coupling to correctness."
        ```
        
        **Over-mocking creates false confidence.** If every collaborator is mocked, you're testing that your code calls its dependencies in the way you *expect* — not that the resulting system actually works when those dependencies are real. This is exactly the earlier problem we found: a `Mock()`-based repository test proves nothing about whether `.get()` after `.add()` actually returns the right thing, because you configured `.get.return_value` by hand.
        
        ## Why fakes largely avoid this criticism
        
        A fake doesn't care how many times you called it or in what order — it just tracks real state and answers correctly based on that state. Your test can refactor the *internal* call pattern of `place_order` freely, and as long as the **outcome** (order ends up PAID, committed) is still correct, the test still passes. That's testing behavior, not implementation.
        
        ## The decision rule, restated cleanly
        
        | Situation | Use | Why |
        | --- | --- | --- |
        | Dependency has real state your code relies on (repository, unit of work) | **Fake** | You can test outcomes, not call patterns — refactor-safe |
        | Dependency just needs to hand back a fixed value to drive a branch (e.g., "pretend the payment succeeded/failed") | **Stub** | Simple, no real logic needed, and you're not asserting on the call itself |
        | The dependency is a pure side effect with genuinely no resulting state to inspect (send email, publish to a queue, call a logging service) | **Mock** | There is nothing else *to* test — the interaction IS the entire observable behavior |
        
        ## The practical guidance, matching the "Classical school" from our research
        
        Default to **fakes and stubs**. Reach for a **mock only when there's truly no state to assert against otherwise** — this was exactly the email-sending example: you can't check "was an email actually sent" any other way, so verifying the call is the only meaningful test available. That's a legitimate, narrow use of mocks, not the general-purpose tool.
        
        The criticism you've heard is really aimed at teams that mock *everything*, including things like repositories and domain collaborators that have perfectly good state to test against instead — that's the London/Mockist school's known failure mode (fragile, over-specified tests that break on every refactor). Using fakes for anything with real state, and reserving mocks for genuine side-effect-only boundaries, sidesteps that criticism almost entirely.
        
    - Why prefer service level test to domain level test
        
        **Book alignment note (Cosmic Python, ch. 5 — "TDD in High Gear and Low Gear"):** the split above is a coverage-ownership argument — each layer owns testing its own branches. There's a second, independent reason to lean the *bulk* of test-writing toward `service_layer/` rather than `domain/`, even where both could technically prove the same fact:
        
        **Every test that calls a domain object's methods directly and asserts on its internal state is coupling — glue holding the implementation in a fixed shape.** A service-layer test that only calls the public, primitive-typed service function (`place_order(order_id="...", amount_cents=..., ...)`) and only asserts on the *outcome* (what the repository ends up holding) doesn't know or care whether `Order` mutates in place, returns a new object, or is replaced by a state machine tomorrow. A domain test that calls `order.mark_as_paid()` and asserts `order.status == PAID` does know all of that, and breaks the moment any of it changes — even when the actual business behavior ("a paid order ends up paid") hasn't changed at all.
        
        **Concrete example from `Order.mark_as_paid()`:**
        
        ```python
        # Coupled — knows the method name AND the internal field it mutates:
        def test_mark_as_paid_changes_status_from_pending_to_paid():
            order = Order(id="order-1", amount_cents=2500, customer_reference="cust-abc")
            order.mark_as_paid()
            assert order.status == OrderStatus.PAID
        
        # Not coupled to how Order achieves this — only to the service layer's
        # stable, primitive-typed contract and the observable outcome:
        def test_successful_purchase_marks_order_paid_and_commits():
            uow = FakeUnitOfWork()
            gateway = FakePaymentGateway(should_succeed=True)
            notifications = FakeNotifications()
        
            place_order(
                order_id="order-1", amount_cents=2500, customer_reference="cust-abc",
                uow=uow, payment_gateway=gateway, notifications=notifications,
            )
        
            saved_order = uow.orders.get("order-1")
            assert saved_order.status == OrderStatus.PAID
        ```
        
        If `Order` is refactored tomorrow to be immutable (`order = order.with_status(PAID)` instead of mutating in place), the second test doesn't change at all. The first one breaks immediately, for a reason that has nothing to do with whether the business logic is correct.
        
        **The resulting default: write as many scenarios as possible at the service layer, and only add a domain test for a pathway the service layer structurally cannot reach.** `place_order` only ever constructs a fresh `PENDING` order and calls `mark_as_paid()` exactly once — so it never exercises "already-paid order gets `mark_as_paid()` called again" (idempotency). That branch is invisible to every service-layer test, no matter how many you write, so it's the kind of pathway that earns a dedicated domain test — not a duplicate of ground the service layer already covers, but a genuine gap. Where a domain branch *is* already fully reached and proven through a service-layer test (as with the fresh-order-to-PAID transition above), don't also keep a domain test for it — that's coupling with no added confidence, and it's the first thing to delete once you notice it.
        
        This doesn't mean domain code goes unverified — it means the verification comes, wherever possible, through a lens that doesn't care how the domain object is implemented internally, with domain-level tests reserved specifically for the branches that lens can't see.
        
    
    ## 1. Test folder structure
    
    ```
    tests/
    ├── unit/
    │   ├── domain/
    │   │   ├── test_order.py
    │   │   └── test_payment_errors.py
    │   ├── service_layer/
    │   │   ├── test_auth.py
    │   │   ├── test_purchase.py
    │   │   └── test_assistant.py
    │
    ├── integration/
    │   ├── db/
    │   │   ├── conftest.py
    │   │   ├── test_repository_contract.py
    │   │   └── test_unit_of_work.py
    │   ├── payment_gateway/
    │   │   └── test_stripe_gateway.py
    │   ├── auth/
    │   │   └── test_clerk_authenticator.py
    │   └── ai/
    │       └── test_openai_assistant.py
    │
    ├── e2e/
    │   ├── conftest.py
    │   ├── test_place_order_flow.py
    │   └── test_assistant_flow.py
    │
    ├── fakes/
    │   ├── fake_order_repository.py
    │   ├── fake_unit_of_work.py
    │   ├── fake_payment_gateway.py
    │   ├── fake_notifications.py
    │   ├── fake_authenticator.py
    │   └── fake_assistant.py
    │
    └── conftest.py
    ```
    
    **Why this shape, not a strict 1:1 mirror of source files:** `domain/` and `service_layer/` have zero I/O, so they're tested *only* at the unit level. `integrations/` has zero meaningful logic to unit test (it's mostly "call the SDK, translate the exception") — it's tested almost entirely at the integration level. So the test tree is organized by **test type first**, with source structure repeated only inside each type where relevant. `fakes/` is centralized once, not duplicated per test file, so there's one place to update when a contract (`AbstractOrderRepository`, etc.) changes.
    
    ## 2. Unit tests — strategy: near-100% branch coverage, via partitioning
    
    > **Default toward `service_layer/`, not `domain/`.** Given the coupling argument above, treat `service_layer/` as where the bulk of unit-test writing effort goes — it's where new business scenarios should be tested first, since a service-layer test covers the same real-world outcome as a domain test while staying refactor-safe. Only add or keep a `domain/` test for a pathway the service layer cannot reach through any of its own call paths (see the idempotency example above). This is a change in *emphasis*, not a change in the mechanics below — near-100% branch coverage still applies to whichever layer actually owns a given branch.
    > 
    
    **Where:** `domain/` and `service_layer/` only. Never `integrations/`'s network-calling methods — mocking third-party SDK internals there produces brittle tests that verify "did I call the mock" rather than real behavior.
    
    **Technique:** equivalence partitioning + boundary value analysis for domain fields (e.g., `amount_cents`: valid positive value, `0` boundary, negative — decide the rule and test it explicitly rather than leaving it undefined). For `service_layer/`, branch-cover every distinct outcome path: success, each specific exception type, each side effect.
    
    **Target:** near-100% branch coverage here specifically, because these tests are cheap (milliseconds, no I/O) and this is where actual business rules live — there's no cost trade-off forcing selectivity.
    
    ## `tests/unit/domain/test_order.py`
    
    > **Exception, not the default:** per the coupling note above, domain tests are kept small and are expected to shrink as service-layer coverage grows — don't treat this file as a template to replicate exhaustively for every future domain object. `mark_as_paid`'s already-paid branch below is kept here specifically because `place_order` never calls `mark_as_paid()` on an order that's already `PAID`, so no service-layer test can ever exercise it. If a future domain method's every branch is already reachable through some service-layer call path, skip writing a dedicated domain test for it.
    > 
    
    ```python
    import pytest
    from domain.order import Order
    from domain.order_status import OrderStatus
    
    def test_mark_as_paid_changes_status_from_pending_to_paid():
        order = Order(id="order-1", amount_cents=2500, customer_reference="cust-abc")
        order.mark_as_paid()
        assert order.status == OrderStatus.PAID
    
    def test_mark_as_paid_on_already_paid_order_stays_paid():
        order = Order(
            id="order-1", amount_cents=2500, customer_reference="cust-abc", status=OrderStatus.PAID
        )
        order.mark_as_paid()
        assert order.status == OrderStatus.PAID
    ```
    
    ## `tests/unit/domain/test_payment_errors.py`
    
    ```python
    nothing worth testing
    ```
    
    ## Why each test earns its place (mapping back to the partitioning rule)
    
    - **`test_order.py`**: covers the one real branch (`mark_as_paid`'s state transition), the one boundary worth flagging explicitly (already-paid), and the input partitions on `amount_cents` (positive/zero/negative) — with the negative case deliberately written as a comment-flagged gap rather than silently assumed correct, since there's no validation logic to test yet.
    - **`test_payment_errors.py`**: since this file has no logic at all (just class definitions), the tests instead verify the **contract** other code relies on — that the subclass relationship holds and behaves correctly when caught, since that's the entire reason this hierarchy exists (so `service_layer/purchase.py` can distinguish `PaymentDeclinedError` from `ProviderUnavailableError` while still allowing broader `except PaymentError` handling elsewhere if needed).
    
    ## Unit test for integration code
    
    **Fakes used by unit tests:**
    
    ```python
    # tests/fakes/fake_order_repository.py
    from integrations.db.repository import AbstractOrderRepository, DuplicateOrderError
    from domain.order import Order
    
    class FakeOrderRepository(AbstractOrderRepository):
        def __init__(self):
            self._orders: dict[str, Order] = {}
    
        def add(self, order: Order) -> None:
            if order.id in self._orders:
                raise DuplicateOrderError(order.id)
            self._orders[order.id] = order
    
        def get(self, order_id: str):
            return self._orders.get(order_id)
    ```
    
    > **Why the fake has to change too:** `FakeOrderRepository` is a genuine
    Fake, not a Stub — it's meant to be a smaller, working version of the
    *same* logic `SqlAlchemyOrderRepository` implements, not just a canned
    responder. Now that `add()`'s real behavior includes rejecting duplicate
    ids, the fake has to reject them too, or every unit test that uses the
    fake would be silently exercising a repository contract that no longer
    matches reality — `place_order` could never be unit-tested against a
    duplicate-id scenario at all, since the fake would just overwrite instead
    of raising. The contract test (below) is what continuously verifies the
    fake and the real adapter still agree on this.
    > 
    
    ```python
    # tests/fakes/fake_unit_of_work.py
    from integrations.db.unit_of_work import AbstractUnitOfWork
    from tests.fakes.fake_order_repository import FakeOrderRepository
    
    class FakeUnitOfWork(AbstractUnitOfWork):
        def __init__(self):
            self.orders = FakeOrderRepository()
            self.committed = False
    
        def __enter__(self):
            return self
    
        def __exit__(self, *args):
            pass
    
        def commit(self):
            self.committed = True
    
        def rollback(self):
            pass
    ```
    
    ```python
    # tests/fakes/fake_payment_gateway.py
    from integrations.payment_gateway import AbstractPaymentGateway, PaymentResult
    
    class FakePaymentGateway(AbstractPaymentGateway):
        def __init__(self, should_succeed: bool = True):
            self.should_succeed = should_succeed
            self.charged_amount = None
            self.charged_customer = None
    
        def charge(self, amount_cents: int, customer_reference: str) -> PaymentResult:
            self.charged_amount = amount_cents
            self.charged_customer = customer_reference
            return PaymentResult(success=self.should_succeed, transaction_id="fake-txn-123")
    ```
    
    - The part that makes it a stub
        
        ```python
        return PaymentResult(success=self.should_succeed, transaction_id="fake-txn-123")
        ```
        
        Regardless of what `amount_cents` or `customer_reference` you pass in, this line returns the same fixed outcome — `success` was decided in advance (in `__init__`, via `should_succeed`), and `transaction_id` is always the literal string `"fake-txn-123"`. The method isn't *computing* a result based on real charge logic (talking to a bank, checking a balance, applying fraud rules) — it's just handing back a canned answer you configured ahead of time. That's the defining trait of a stub: **fixed output regardless of real processing**.
        
    
    ```python
    # tests/fakes/fake_notifications.py
    from integrations.notifications import AbstractNotifications
    
    class FakeNotifications(AbstractNotifications):
        def __init__(self):
            self.sent_receipts: list[str] = []
    
        def send_receipt_email(self, order_id: str) -> None:
            self.sent_receipts.append(order_id)
    ```
    
    **Full unit test example — `tests/unit/service_layer/test_purchase.py`:**
    
    ```python
    from service_layer.purchase import place_order
    from tests.fakes.fake_unit_of_work import FakeUnitOfWork
    from tests.fakes.fake_payment_gateway import FakePaymentGateway
    from tests.fakes.fake_notifications import FakeNotifications
    from domain.order_status import OrderStatus
    
    def test_successful_purchase_marks_order_paid_and_commits():
        # Arrange
        uow = FakeUnitOfWork()
        gateway = FakePaymentGateway(should_succeed=True)
        notifications = FakeNotifications()
    
        # Act
        place_order(
            order_id="order-1",
            amount_cents=2500,
            customer_reference="cust-abc",
            uow=uow,
            payment_gateway=gateway,
            notifications=notifications,
        )
    
        # Assert
        saved_order = uow.orders.get("order-1")
        assert saved_order is not None
        assert saved_order.status == OrderStatus.PAID
        assert saved_order.amount_cents == 2500
        assert uow.committed is True
        assert gateway.charged_amount == 2500
        assert gateway.charged_customer == "cust-abc"
        assert notifications.sent_receipts == ["order-1"]
    ```
    
    Other branches in the same file, following the same pattern (declined payment → no commit; provider unavailable → exception propagates; each with its own `Fake...` configuration).
    
    - Why need both test_order.py and this service test
        
        ## What this one service-layer test actually proves, and what it doesn't
        
        ```python
        assert saved_order.status == OrderStatus.PAID
        ```
        
        This line only tells you: **"after calling `place_order`, the resulting order object happens to have `status == PAID`."** It does NOT tell you *why* — it could be PAID because:
        
        - `place_order` correctly called `order.mark_as_paid()`, which correctly set `self.status = OrderStatus.PAID` ✅ (the real, intended reason)
        - OR `place_order` never called `mark_as_paid()` at all, and just constructed a fresh `Order` with `status=OrderStatus.PAID` directly, bypassing the method entirely
        - OR `mark_as_paid()` has a bug but it happens to not matter for *this specific* test's setup
        
        The service-layer test cannot distinguish between these. It only checks the **final outcome**, not **which code path produced it, or whether that method is correct in general**.
        
        ## Now trace what happens without `test_order.py`
        
        Suppose `mark_as_paid()` is refactored later:
        
        ```python
        def mark_as_paid(self):
            if self.status == OrderStatus.PAID:
                return  # someone adds this "optimization"
            self.status = OrderStatus.PENDING  # <-- typo: should be PAID
        ```
        
        Run `test_successful_purchase_marks_order_paid_and_commits()` against this buggy version: it starts with a fresh `PENDING` order, calls `mark_as_paid()` once, hits the typo'd line, and fails — **so in this particular scenario, yes, the service-layer test does catch it.**
        
        But now consider: does the service layer ever call `mark_as_paid()` on an order that's *already* PAID? Look at `place_order`'s code — no, it always constructs a brand-new `Order()` (default PENDING) and calls `mark_as_paid()` once. **The service-layer test never exercises the "already paid → call again" branch at all**, because that scenario never arises in `place_order`'s own logic.
        
        This is exactly what `test_mark_as_paid_on_already_paid_order_stays_paid()` covers — a branch the service layer literally has no path through. If a future bug only manifests in that branch (e.g., someone adds faulty "idempotency" logic like above, but with a guard that only misbehaves on the *second* call), the service-layer test would never catch it, because `place_order` never calls `mark_as_paid` twice on the same object.
        
        ## So: is it duplication, or is it coverage of different things?
        
        **Genuine overlap exists** for the single specific case both tests happen to share (fresh order → mark_as_paid → PAID). If you deleted `test_mark_as_paid_changes_status_from_pending_to_paid()` specifically, the service-layer test above would still incidentally catch a typo in that exact line, most of the time.
        
        **No overlap exists** for the already-paid boundary case — that's uniquely owned by `test_order.py`, since the service layer never reaches it.
        
        ## The concrete trade-off, stated plainly
        
        | If you keep both | If you drop `test_order.py` |
        | --- | --- |
        | Minor duplication on the "fresh order → paid" case | Lose coverage entirely on the "already paid" boundary case |
        | Cheap — domain tests run in microseconds | Save maybe 5 lines of test code |
        | A domain bug is pinpointed exactly ("mark_as_paid is broken") | A domain bug only surfaces as "place_order produced wrong output," requiring you to dig through the service layer to find it's actually a domain bug |
        
        ## My actual answer
        
        Keep both, but recognize *why*: it's not that they're testing the exact same thing twice for safety margin — it's that `test_order.py` exhaustively covers `Order`'s behavior on its own terms (including branches `place_order` never touches), while the service-layer test only incidentally re-confirms a fragment of that as a side effect of checking orchestration. The "duplication" you're sensing is real but small (one shared scenario), and the cost of keeping it (a 5-line, microsecond test) is far lower than the cost of the blind spot you'd create by removing it (an untested branch, plus losing precise failure localization when something breaks).
        
        > **Reconciling this with the coupling note above:** this specific case is exactly the exception the coupling argument carves out — keep it because of the genuine, service-layer-unreachable "already-paid" gap, not as a general habit of duplicating every domain transition at both layers.
        > 
    
    ## 3. Integration tests — strategy: narrow, only what a fake can't prove
    
    - Rules to follow to not over - integration test
        
        ## The core rule that decides everything
        
        > **Unit tests are scoped by *logic branches*. Integration tests are scoped by *integration points*.**
        > 
        
        A unit test asks: "how many distinct decision paths does this function have?" — and you write one test per path (partitioning, boundaries, exceptions).
        
        An integration test asks: "how many distinct ways can this specific external system surprise us?" — and you write one test per surprise category, **not** one test per business scenario.
        
        ## Rule 1 — Test each CRUD operation once, not every input variation
        
        **Unit test rule:** partition inputs (valid, boundary, invalid) and branch-cover every path.
        
        **Integration test rule:** test that each operation (`add`, `get`, `list`, `update`, `delete`) works *at all* against the real system — once, with one representative input. You are not re-testing input validation here; that already happened in the unit tests, using the fake.
        
        ```python
        # ✅ Enough for integration:
        def test_can_add_and_get_an_order(repo): ...
        
        # ❌ Don't also add these to integration — they belong in unit tests:
        def test_add_with_zero_amount(repo): ...
        def test_add_with_negative_amount(repo): ...
        def test_add_with_extremely_long_customer_reference(repo): ...
        ```
        
        ## Rule 2 — Only test what only the real system can produce
        
        Ask: "could a `dict`/`set`-backed fake ever produce this exact behavior?" If yes, it doesn't belong in integration tests — you already covered it. If no, that's exactly the integration test's reason to exist.
        
        | Scenario | Could a fake reproduce this? | Test it in |
        | --- | --- | --- |
        | "get on missing key returns None" | Debatable — write once in contract test | Integration (contract test) |
        | "duplicate order ID raises DuplicateNoteError" | Yes, pure Python logic | Unit |
        | "enum column round-trips correctly through SQL" | No — fake stores the live object, never serializes | Integration |
        | "transaction rolls back on exception" | No — fake `rollback()` is `pass` | Integration |
        | "declined card raises PaymentDeclinedError" | No — need Stripe's real error shape | Integration |
        | "amount_cents=0 is/isn't allowed" | Yes, pure logic | Unit |
        
        ## Rule 3 — One test per exception/error mapping, not per cause
        
        For each adapter, you catch a small, fixed set of exception types and translate them. Test **one representative trigger per exception type** — not every possible cause of that exception.
        
        ```python
        # Stripe adapter catches exactly 2 categories → exactly 2 integration tests:
        def test_charge_with_valid_card_succeeds(): ...
        def test_charge_with_declined_card_raises_payment_declined(): ...
        # NOT: test with expired card, test with insufficient funds, test with stolen card...
        # those all hit the SAME except block and produce the SAME PaymentDeclinedError —
        # testing them individually just re-proves Stripe's own classification, not your code.
        ```
        
        If your adapter's `except` block only branches on exception *type*, not on any content within it, one trigger per type is sufficient.
        
        ## Rule 4 — Unit-of-Work gets exactly 2–3 tests, always the same shape
        
        Because commit/rollback/context-manager behavior is generic across any UoW implementation, this is a fixed, small checklist — not something that grows with your domain:
        
        ```python
        def test_commit_persists_changes(): ...
        def test_rollback_on_exception_discards_changes(): ...
        def test_context_manager_rolls_back_if_not_committed(): ...
        ```
        
        Don't add more scenarios beyond this — anything about *what* was committed/rolled back is a business-logic question, tested at the unit level with `FakeUnitOfWork`.
        
        ## Rule 5 — Contract tests stay minimal: CRUD + missing-key behavior only
        
        3–5 tests, max, per repository. Their only job is proving fake ≈ real. Do not use the contract test file to explore edge cases of your domain model — that's what unit tests with the fake are for.
        
        ## Side-by-side rule comparison
        
        |  | Unit test scope | Integration test scope |
        | --- | --- | --- |
        | **Driven by** | number of logic branches in the function | number of integration points with the external system |
        | **Coverage goal** | near-100% branch coverage | one test per operation + one per distinct failure-translation path |
        | **Input variation** | yes — partition valid/boundary/invalid | no — one representative input is enough |
        | **What triggers a new test** | a new `if`/exception branch in your code | a new external call, a new caught exception type, a new transaction concern |
        | **Typical count for a module** | 5–15+ tests per function, depending on branches | 2–6 tests total per adapter |
        
        ## The practical checklist to apply to any new adapter
        
        1. List every public method → one integration test per method (happy path).
        2. List every exception type the adapter's `except` blocks catch → one integration test per type.
        3. If it's a Unit of Work → the fixed 3-test commit/rollback/context-manager set.
        4. If it's a repository → add the small parametrized contract test (fake + real) for CRUD + missing-key.
        5. Stop there. Anything else — input validation, business rules, combinations of conditions — goes back to unit tests with the fake.
        
        That's the dividing line: **unit tests own the *shape of your logic*; integration tests own the *shape of your integration surface*, one test per distinct thing that could go wrong on the other side of the call.**
        
    
    <aside>
    💡
    
    **What is the difference between integration and fakes for 3rd party services?**
    
    - fakes are for an easy and cheap way to test our 3rd party service logic whenever we want
    - integration test is to check if we are using/integrating the 3rd party services correctly - eg. using the correct function signature, correct config, correct handling of the 3rd party services’ exception
    
    **With these 2 ways of testing, we can also uncover more insights:**
    
    - If unit test using fake passes but integration test fails →There might be bug at the integration of the services
    - If unit test fails but integration test passes → fakes/unit test might be out of drift
    </aside>
    
    **Approximate count:** Fewer than unit tests because each one is slower, sometimes costs money (sandbox API calls), and is more prone to flakiness.
    
    **Contract test — `tests/integration/db/conftest.py` + `test_repository_contract.py`:**
    
    ```python
    # tests/integration/db/conftest.py
    import pytest
    from sqlalchemy import create_engine
    from sqlalchemy.orm import sessionmaker
    from integrations.db.model.order import Base
    
    @pytest.fixture
    def sqlalchemy_session():
        engine = create_engine("sqlite:///:memory:")
        Base.metadata.create_all(engine)
        Session = sessionmaker(bind=engine)
        session = Session()
        yield session
        session.close()
    ```
    
    ```python
    # tests/integration/db/test_repository_contract.py
    import pytest
    from domain.order import Order
    from domain.order_status import OrderStatus
    from integrations.db.repository import SqlAlchemyOrderRepository, DuplicateOrderError
    from tests.fakes.fake_order_repository import FakeOrderRepository
    
    @pytest.fixture(params=["fake", "sqlalchemy"])
    def repo(request, sqlalchemy_session):
        if request.param == "fake":
            return FakeOrderRepository()
        return SqlAlchemyOrderRepository(sqlalchemy_session)
    
    def test_can_add_and_get_an_order(repo):
        order = Order(
            id="order-1",
            amount_cents=2500,
            customer_reference="cust-abc",
            status=OrderStatus.PENDING,
        )
    
        repo.add(order)
        retrieved = repo.get("order-1")
    
        assert retrieved is not None
        assert retrieved.id == "order-1"
        assert retrieved.amount_cents == 2500
        assert retrieved.customer_reference == "cust-abc"
        assert retrieved.status == OrderStatus.PENDING
    
    def test_adding_duplicate_id_raises_duplicate_order_error(repo):
        order = Order(id="order-1", amount_cents=2500, customer_reference="cust-abc")
        repo.add(order)
    
        duplicate = Order(id="order-1", amount_cents=9999, customer_reference="cust-xyz")
        with pytest.raises(DuplicateOrderError):
            repo.add(duplicate)
    ```
    
    This runs twice (fake + real SQLite standing in for Postgres), proving the ORM mapping actually round-trips — something the fake alone can't demonstrate. `test_adding_duplicate_id_raises_duplicate_order_error` is the case the earlier integration-scoping table already anticipated ("duplicate order ID raises DuplicateNoteError" — pure Python logic on the fake side) but the suite hadn't implemented until `add()` grew real duplicate-rejecting behavior; it's also exactly what would catch the fake and the real adapter drifting apart on this specific behavior if one of them were changed without the other.
    
    <aside>
    💡
    
    **Why the contract test matters (fake/real drift):**
    
    **Governing question for every integration test:** "what can only be verified by hitting the real dependency?" ie. All the code that can only be ran by calling external services has to be tested here (integration folder)
    
    If the contract test passes, the fake is trustworthy, and a green unit test suite genuinely reflects correct business logic — not just internal consistency with a stale fake. If it fails, it's a signal that the fake (and likely the unit tests built on it) have drifted from reality and need to be updated to match the real implementation.
    
    1. **Alignment** — that the fake still behaves the same as the real implementation.
    2. **Wiring** — that the real adapter actually integrates correctly with the external system (real SQL, real ORM mapping).
    
    The contract test exists to catch exactly this. By running the same assertions against both implementations, it verifies two things:
    
    If you change `SqlAlchemyOrderRepository`'s behavior but forget to update `FakeOrderRepository` to match, the fake and its unit tests remain internally consistent with each other — so every unit test built on that fake keeps passing, even though it no longer reflects how the real repository actually behaves. Unit tests alone cannot detect this, since they only check the fake against itself.
    
    </aside>
    
    **Unit-of-Work integration test — `test_unit_of_work.py`:**
    
    ```python
    import pytest
    from sqlalchemy import create_engine
    from sqlalchemy.orm import sessionmaker
    from integrations.db.model.order import Base
    from integrations.db.unit_of_work import SqlAlchemyUnitOfWork
    from domain.order import Order
    
    @pytest.fixture
    def session_factory():
        engine = create_engine("sqlite:///:memory:")
        Base.metadata.create_all(engine)
        return sessionmaker(bind=engine)
    
    def test_rollback_on_exception_discards_changes(session_factory):
        uow = SqlAlchemyUnitOfWork(session_factory)
    
        with pytest.raises(ValueError):
            with uow:
                order = Order(id="order-1", amount_cents=1000, customer_reference="cust-x")
                uow.orders.add(order)
                raise ValueError("simulated failure mid-transaction")
    
        fresh_session = session_factory()
        from integrations.db.repository import SqlAlchemyOrderRepository
        fresh_repo = SqlAlchemyOrderRepository(fresh_session)
    
        assert fresh_repo.get("order-1") is None
    ```
    
    Proves real transactional rollback — `FakeUnitOfWork.rollback()` is just `pass`, structurally incapable of showing this.
    
    **Stripe integration test:**
    
    ```python
    import os
    import pytest
    from integrations.payment_gateway import StripePaymentGateway
    from domain.payment_errors import PaymentDeclinedError
    
    STRIPE_TEST_DECLINED_TOKEN = "tok_chargeDeclined"
    
    @pytest.fixture
    def stripe_gateway():
        return StripePaymentGateway(api_key=os.environ["STRIPE_TEST_API_KEY"])
    
    def test_charge_with_declined_test_card_raises_payment_declined(stripe_gateway):
        with pytest.raises(PaymentDeclinedError):
            stripe_gateway.charge(
                amount_cents=2500,
                customer_reference=STRIPE_TEST_DECLINED_TOKEN,
            )
    ```
    
    Hits Stripe's real sandbox, proving our adapter correctly translates Stripe's real error into our own exception type.
    
    **Clerk integration test:**
    
    ```python
    import os
    import pytest
    from integrations.auth.clerk import ClerkAuthenticator, AuthenticationError
    
    @pytest.fixture
    def clerk_authenticator():
        return ClerkAuthenticator(
            clerk_jwks_url=os.environ["CLERK_TEST_JWKS_URL"],
            clerk_issuer=os.environ["CLERK_TEST_ISSUER"],
        )
    
    def test_expired_token_raises_authentication_error(clerk_authenticator, expired_test_jwt):
        with pytest.raises(AuthenticationError):
            clerk_authenticator.verify_token(expired_test_jwt)
    ```
    
    **OpenAI integration test** (uses `mock.patch` deliberately, since simulating a real network failure against a live API isn't practical):
    
    ```python
    import pytest
    from unittest import mock
    from openai import APIConnectionError
    from integrations.ai.openai_client import OpenAIAssistant
    from integrations.errors import ProviderUnavailableError
    
    def test_provider_error_maps_to_provider_unavailable():
        assistant = OpenAIAssistant(api_key="test-key")
    
        with mock.patch.object(
            assistant._client.chat.completions, "create",
            side_effect=APIConnectionError(request=mock.Mock()),
        ):
            with pytest.raises(ProviderUnavailableError) as exc_info:
                assistant.generate_reply("Hello")
    
            assert exc_info.value.provider == "openai"
    ```
    
    ## 4. End-to-end tests — strategy: fewest, one per user flow, wiring only
    
    - Rules to not over - e2e test
        
        ## The core rule that decides everything
        
        > **E2E tests are scoped by *wiring paths through the whole stack*, not by logic branches and not by integration-point failure modes.**
        > 
        
        Unit tests ask "how many decision branches does this function have?" Integration tests ask "how many ways can this external system surprise us?" E2E tests ask: **"how many distinct routes does a request take through the fully assembled app, and does each one actually connect end to end?"**
        
        ## Rule 1 — One test per user-facing flow, not per business scenario
        
        1 per route → Each route really can break independently at the wiring level even if every other route works, so testing each one once is legitimate, not redundant.
        
        A "flow" = one complete round trip: HTTP request in → middleware → route → service layer → real adapters → HTTP response out. You get **one happy-path test per flow**, full stop.
        
        ```python
        # ✅ Enough for e2e:
        def test_full_purchase_flow_returns_200(): ...   # covers /orders
        def test_full_assistant_flow_returns_200(): ...  # covers /assistant
        
        # ❌ Don't add these here — already covered elsewhere:
        def test_purchase_with_declined_card(): ...      # already integration-tested (Stripe adapter)
        def test_purchase_with_zero_amount(): ...        # already unit-tested (domain/service logic)
        ```
        
        If you find yourself writing a second e2e test for the *same* route just to check a different business outcome, stop — that's a signal the test belongs in unit or integration instead.
        
        ## Rule 2 — Only test failures that are about wiring, not logic
        
        Ask: "would this failure exist even if every individual component (route, service, adapter) were already proven correct in isolation?" If yes → e2e. If no → it's already covered.
        
        | Scenario | Is this a wiring concern? | Test it in |
        | --- | --- | --- |
        | Route not registered / wrong HTTP method | Yes — only visible when the whole app boots | E2E |
        | Middleware blocking/allowing requests incorrectly | Yes — middleware only exists in the assembled app | E2E |
        | `bootstrap()` passes wrong argument order to a service | Yes — only surfaces when DI actually runs | E2E |
        | Missing auth token → 401 | Yes — this is `AuthMiddleware`'s job specifically | E2E |
        | Declined card → 402 | No — Stripe's error shape already integration-tested | Integration |
        | Duplicate order ID → exception | No — pure business logic | Unit |
        
        ## Rule 3 — Auth/middleware gets a small fixed checklist, same idea as Unit-of-Work in integration tests
        
        Because middleware behavior is generic across every protected route, it doesn't grow with your domain — test it once, thoroughly, then trust it everywhere else:
        
        ```python
        def test_request_without_token_returns_401(): ...
        def test_request_with_valid_token_succeeds(): ...
        def test_public_path_skips_auth(): ...
        ```
        
        3 tests, not one per protected route. You don't need `test_orders_without_token_401` AND `test_assistant_without_token_401` — that's testing the same middleware twice.
        
        ## Rule 4 — One combined unhappy-path test per route, not one test per status code
        
        <aside>
        💡
        
        **Why need to do this when we have done it at integration test?**
        Testing unhappy path at e2e level checks if the error is translated to the correct status/error code 
        
        </aside>
        
        ```python
        # NOT this (one test per status code):
        def test_declined_payment_returns_402(): ...
        def test_provider_down_returns_503(): ...
        
        # THIS (one combined, parametrized unhappy-path test per route):
        @pytest.mark.parametrize(
            "customer_reference, expected_status",
            [
                ("tok_chargeDeclined", 402),      # PaymentDeclinedError -> 402
                ("tok_forceProviderDown", 503),   # ProviderUnavailableError -> 503
            ],
        )
        def test_unhappy_paths_return_correct_status(customer_reference, expected_status): ...
        ```
        
    
    **What's being tested:** not business logic (already covered by unit tests), not adapter correctness (already covered by integration tests) — only that the whole stack is wired together correctly: routing, middleware, `bootstrap()`'s dependency injection, request/response cycle.
    
    **Approximate count:** 3–6 total for this app.
    
    ```python
    # tests/e2e/conftest.py
    import pytest
    from fastapi.testclient import TestClient
    from entrypoints.api.app import create_app
    
    @pytest.fixture
    def test_client():
        app = create_app()
        return TestClient(app)
    ```
    
    ```python
    # tests/e2e/test_place_order_flow.py
    def test_full_purchase_flow_returns_200_and_confirms_order(test_client, valid_test_jwt):
        response = test_client.post(
            "/orders",
            json={
                "order_id": "order-e2e-1",
                "amount_cents": 1500,
                "customer_reference": "tok_visa",
            },
            headers={"Authorization": f"Bearer {valid_test_jwt}"},
        )
    
        assert response.status_code == 200
        body = response.json()
        assert body["status"] == "ok"
        assert "created_by" in body
    
    @pytest.mark.parametrize(
        "customer_reference, expected_status",
        [
            ("tok_chargeDeclined", 402),      # PaymentDeclinedError -> 402
            ("tok_forceProviderDown", 503),   # ProviderUnavailableError -> 503
        ],
    )
    def test_unhappy_paths_return_correct_status(
        test_client, valid_test_jwt, customer_reference, expected_status
    ):
        response = test_client.post(
            "/orders",
            json={
                "order_id": "order-e2e-2",
                "amount_cents": 1500,
                "customer_reference": customer_reference,
            },
            headers={"Authorization": f"Bearer {valid_test_jwt}"},
        )
    
        assert response.status_code == expected_status
    ```
    
    ## 5. Test pyramid summary
    
    | Layer | Approx. count | Speed | Runs against |
    | --- | --- | --- | --- |
    | Unit | 40–80+ | milliseconds | fakes only, zero I/O |
    | Integration | 15–25 | seconds each | real test DB, sandbox/test-mode third parties |
    | E2E | 3–6 | seconds–low minutes | full app, real test DB, sandbox third parties |
    
    ## 6. CI trigger strategy — when each suite runs
    
    - Why we need both `required check before merge` and on `merge to main` (seem identical)
        
        ## Why they seem identical
        
        "Required check before merge" runs tests against the **PR branch merged with main** (most CI systems, like GitHub Actions with `pull_request` triggers, actually test the *merge result*, not just your branch in isolation). "On merge to main" runs tests against **main after the merge actually happened**. If nothing else changes in between, these should produce the exact same code and the exact same test results.
        
        ## The gap: things can change *between* those two moments
        
        1. **Another PR merges first.** Say PR A and PR B are both open, both pass their pre-merge checks independently. PR A merges. Now PR B's pre-merge check was run against an *older* version of main — before PR A's changes. If PR A and PR B touch overlapping code in a way that conflicts (even without a git merge conflict — e.g., PR A changes a function's behavior that PR B's tests implicitly depended on), PR B's pre-merge check never actually saw that interaction. The post-merge run on main is the first time the *combined* result of A+B is actually tested.
        2. **Time-based/flaky dependencies drift.** If a test depends on something that changes over time (a third-party sandbox API version, an expiring test credential, a dependency version resolving differently on a fresh install), the pre-merge check and the post-merge check could genuinely produce different results even for identical code — because the *environment* isn't identical, only the code is.
        3. **Branch protection isn't airtight everywhere.** Not every repo enforces "PR must be up-to-date with main before merging" strictly. If that setting is off, your PR could merge while several commits behind main, meaning your pre-merge check tested against stale code — the post-merge run is the actual first true test of what's now on main.
        
        ## When it genuinely IS pure redundancy
        
        If your repo has:
        
        - branch protection requiring the PR to be up-to-date with main before merge (no other commits can sneak in), and
        - a strict merge queue (serializes merges one at a time, always re-testing against the latest main before each merge)
        
        ...then yes, "on merge to main" becomes redundant in the common case — many teams running a proper merge queue (e.g., GitHub's merge queue feature) skip a separate "on merge" test run for this exact reason, since the merge queue already re-validates immediately before merging.
        
        ## The practical takeaway for your table
        
        State it as conditional, not a blanket rule:
        
        > "On merge to main" is only necessary as a *separate* run if your repo allows merging without the PR being fully up-to-date with main, or without a merge queue. If you enforce strict up-to-date-before-merge + a merge queue, the pre-merge check already covers this and the post-merge run is redundant and can be dropped (or kept only as a lightweight sanity smoke test, not the full suite).
        > 
    
    | Trigger | Unit | Integration | E2E |
    | --- | --- | --- | --- |
    | Every push to a PR branch | ✅ always | ✅ always (suite small enough to stay fast) | ❌ not every push |
    | Required check before merge
    (optional) | ✅ | ✅ | ✅ — this is the gate |
    | On merge to main | ✅ | ✅ | ✅ |
    | Nightly/scheduled | optional | ✅ full sweep | ✅ full sweep, possibly against staging |
    | Before production deploy | — | — | ✅ smoke-test subset against staging |
    
    <aside>
    💡
    
    **If we enforce our repo to resolve merge conflict with main before merging, we do not need `required check before merge`**
    
    **Reasoning:** unit tests are near-free to run constantly, so they run on every push with no exceptions. This integration suite is small enough (15–25 tests) to also run on every push; if it grows large or flaky (e.g., OpenAI rate limits, external service health), move it to PR-open + pre-merge only. E2E tests are slowest and most flake-prone (real network, sandbox rate limits), so they gate the merge rather than firing on every intermediate WIP push. Nightly runs catch drift unrelated to your own code changes (a sandbox API changing behavior, an expired test key). Pre-deploy smoke tests are the final cheap insurance that the specific deployed build and config are correctly wired before it serves real traffic.
    
    </aside>