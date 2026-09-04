- Part 2: Event-Driven Architecture
    
    > Same six chapters and the same prose structure as the original notes (Problem → Solution →
    code → diagram → Trade-off). What's different: the domain is `Patient` instead of `Product`,
    and every code change is shown as an explicit **BEFORE** (explanation + code) and **AFTER**
    (explanation + code) pair.
    > 
    
    ## Domain mapping
    
    | Allocation version | Patient version | What it is |
    | --- | --- | --- |
    | `Product` | `Patient` | the **aggregate root** (identity: `patient_id`) |
    | `Batch` | `Appointment` | a **composite entity** owned by the root |
    | `OrderLine` | `TimeSlot` | a **value object** (the requested slot) |
    | — | `PhoneNumber` | a second **value object** on the root |
    | `product.allocate(line)` | `patient.book(slot)` | the core domain operation |
    | `Allocated` | `AppointmentBooked` | **success event** (a fact) |
    | `OutOfStock` | `SchedulingConflict` | **failure event** (a fact) |
    | `Allocate` / `AllocationRequired` | `BookAppointment` | **command** (a request) |
    | `CreateBatch` | `RegisterPatient` | command that creates an aggregate |
    | `ChangeBatchQuantity` | `CancelAppointment` | inbound cross-service command |
    | `allocations_view` | `appointments_view` | CQRS read model |
    
    The invariant the aggregate enforces: **a patient cannot be double-booked.** Only `Patient`
    can check that, because only it can see *all* of that patient's appointments at once — which
    is why the booking decision lives on the root and nowhere else.
    
    ---
    
    ## Ch. 8–9 — Events and the Message Bus
    
    **Problem:** New side effects (email on a scheduling conflict, SMS alerts, logging, updating a
    read model) kept getting bolted directly into the service layer, coupling business
    orchestration to infrastructure calls. As more than one thing needs to happen per action, that
    same function becomes a growing list of manual calls, and "service layer function" starts
    looking suspiciously like the same shape as "event handler."
    
    **Solution:** The domain model records events as plain data (facts about what happened). A
    message bus looks up handlers for each event type and runs them — the domain model never
    calls infrastructure directly. The bus itself becomes the actual entry point for everything,
    even the original request: service layer functions become handlers themselves, registered
    against events, and the bus loops until no new events remain instead of running once and
    stopping.
    
    ### BEFORE — no events exist yet
    
    The domain model only returns a value or `None`. Any reaction to failure (sending an email)
    has to be written directly into the same function that does the booking, importing the email
    library right there. If you later want a second reaction — say, also logging the conflict, or
    notifying another service — you edit this same function again, and Flask calls it directly by
    name.
    
    ```python
    # entrypoints/flask_app.py — BEFORE
    from flask import Flask, request
    from service_layer import services
    
    app = Flask(__name__)
    
    @app.route("/book", methods=["POST"])
    def book_endpoint():
        ref = services.book(
            request.json["patient_id"], request.json["slot_time"], uow
        )
        return {"reference": ref}, 201
    ```
    
    ```python
    # service_layer/services.py — BEFORE
    from domain.model import TimeSlot
    from adapters.email import send_mail   # infrastructure imported straight into orchestration
    
    def book(patient_id, slot_time, uow):
        with uow:
            patient = uow.patients.get(patient_id)
            slot = TimeSlot(slot_time)
            ref = patient.book(slot)
            uow.commit()
        if ref is None:
            send_mail("scheduling@clinic.example", f"Conflict for patient {patient_id}")
            # ^ hard dependency on email, sitting inside business orchestration code
        return ref
    ```
    
    ### AFTER — the domain records facts, the bus is the only entry point, and it forwards for you
    
    `Patient.book` no longer knows or cares what should happen on success or failure — it just
    appends plain data objects (`AppointmentBooked`, `SchedulingConflict`) describing what
    happened to its own `events` list. Nothing about email or Redis is mentioned anywhere near the domain model. The old `services.book` is renamed `book_appointment`, moved into `messageHandlers.py`, and no longer does any manual forwarding. `messagebus.handle` owns a dictionary mapping message types to the functions that react to them, grows a queue and a `while` loop, and keeps dispatching until no new events remain — pulling freshly-produced ones from the unit of work after every handler runs. Flask no longer knows `services.py` or any handler function by name; it just builds a request object and hands it to the bus.
    
    ```python
    # domain/events.py — AFTER
    from dataclasses import dataclass
    
    @dataclass
    class AppointmentBooked:
        patient_id: str
        slot_time: str
        reference: str
    
    @dataclass
    class SchedulingConflict:
        patient_id: str
    ```
    
    ```python
    # domain/model.py — AFTER
    from domain.events import AppointmentBooked, SchedulingConflict
    
    class Patient:
        def __init__(self, patient_id, appointments):
            self.patient_id = patient_id
            self.appointments = appointments
            self.events = []                       # a place to record facts
    
        def book(self, slot: TimeSlot) -> str:
            if any(appt.conflicts_with(slot) for appt in self.appointments):
                self.events.append(SchedulingConflict(self.patient_id))
                return None
            appt = Appointment(slot)
            self.appointments.append(appt)
            self.events.append(
                AppointmentBooked(self.patient_id, slot.time, appt.reference)
            )
            return appt.reference
    ```
    
    ```python
    # service_layer/messageHandlers.py — AFTER (replaces services.py)
    from domain.model import TimeSlot
    from domain.events import AppointmentBooked, SchedulingConflict
    from domain.commands import BookAppointment
    from adapters.email import send_mail
    from adapters import redis_eventpublisher
    
    def book_appointment(cmd: BookAppointment, uow) -> str:   # renamed from `book`
        with uow:
            # aggregate
            patient = uow.patients.get(cmd.patient_id)   # pulled from the message object
            slot = TimeSlot(cmd.slot_time)
            ref = patient.book(slot)                      # calls the DOMAIN method on the root
            uow.commit()
        return ref
        # no manual forwarding loop here — that's the bus's job (see collect_new_events below)
    
    def send_conflict_notification(event: SchedulingConflict, uow):
        send_mail("scheduling@clinic.example", f"Conflict for patient {event.patient_id}")
    
    def publish_appointment_booked_event(event: AppointmentBooked, uow):
        redis_eventpublisher.publish("appointment_booked", event)
    ```
    
    ```python
    # service_layer/messagebus.py — AFTER
    from service_layer import messageHandlers
    from domain.commands import BookAppointment
    from domain.events import AppointmentBooked, SchedulingConflict
    
    HANDLERS = {
        BookAppointment: [messageHandlers.book_appointment],
        AppointmentBooked: [messageHandlers.publish_appointment_booked_event],
        SchedulingConflict: [messageHandlers.send_conflict_notification],
    }
    
    def handle(message, uow):
        queue = [message]                              # start with whatever was handed in
        while queue:                                    # keep going until nothing is left
            message = queue.pop(0)                      # take from the front (BFS)
            for handler in HANDLERS[type(message)]:
                handler(message, uow)
            queue.extend(uow.collect_new_events())      # handlers can raise more events
    ```
    
    ```python
    # entrypoints/flask_app.py — AFTER
    from flask import Flask, request
    from service_layer import messagebus
    from domain.commands import BookAppointment
    
    app = Flask(__name__)
    
    @app.route("/book", methods=["POST"])
    def book_endpoint():
        cmd = BookAppointment(
            request.json["patient_id"], request.json["slot_time"]
        )
        messagebus.handle(cmd, uow)
        return "OK", 202
    ```
    
    **What actually changed:** Flask went from calling a named function (`services.book`) to
    building a message (`BookAppointment`) and handing it to a generic dispatcher
    (`messagebus.handle`) — Flask no longer knows or cares what function eventually runs, or how many events fire as a consequence.
    
    **The dependency direction**
    
    ```
    Before: service.py → email library (hard dependency), called directly by Flask
    After:  domain model → records Events (plain data)
            messagebus   → the only entry point; looks up handlers → handlers may raise MORE
                            events → bus keeps processing until the queue is empty
    ```
    
    ### Where the events come from at the end of the loop — `.seen` and `collect_new_events`
    
    `queue.extend(uow.collect_new_events())` has to find every event raised during
    `book_appointment` — but by the time it runs, that function has already returned and its local `patient` variable is gone. Two more BEFORE/AFTER pairs make that possible.
    
    **BEFORE — `adapters/repository.py`, no tracking of what was touched:**
    
    ```python
    # adapters/repository.py — BEFORE
    import abc
    from domain import model
    
    class AbstractRepository(abc.ABC):
        @abc.abstractmethod
        def add(self, patient: model.Patient): ...
    
        @abc.abstractmethod
        def get(self, patient_id) -> model.Patient: ...
        # nothing remembers which patients were fetched/added this transaction
    ```
    
    **AFTER — the repository remembers every aggregate it handed out:**
    
    `add` and `get` stop being abstract — they become real methods, written once,
    that call a new `_add`/`_get` (which subclasses implement) and then unconditionally record theobject into a `.seen` set. Subclasses can no longer forget to track anything, because theynever touch `.seen` themselves — it happens automatically, every time, in the base class.
    
    ```python
    # adapters/repository.py — AFTER
    import abc
    from domain import model
    
    class AbstractRepository(abc.ABC):
        def __init__(self):
            self.seen = set()                # every aggregate this repo handed out/stored
    
        def add(self, patient: model.Patient):
            self._add(patient)
            self.seen.add(patient)           # tracked automatically
    
        def get(self, patient_id) -> model.Patient:
            patient = self._get(patient_id)
            if patient:
                self.seen.add(patient)       # tracked automatically
            return patient
    
        @abc.abstractmethod
        def _add(self, patient: model.Patient): ...    # subclasses implement THESE instead
    
        @abc.abstractmethod
        def _get(self, patient_id) -> model.Patient: ...
    ```
    
    **BEFORE — `service_layer/unit_of_work.py`, no way to gather events after the fact:**
    
    ```python
    # service_layer/unit_of_work.py — BEFORE
    import abc
    
    class AbstractUnitOfWork(abc.ABC):
        def __exit__(self, *args):
            self.rollback()
    
        def commit(self):
            self._commit()
        # no collect_new_events() — nothing needed it yet, since services.py forwarded manually
    ```
    
    **AFTER — drains events from everything the repository saw:**
    
    A new method loops over every aggregate root the repository tracked in `.seen`
    and drains each one's `.events` list one item at a time (`pop(0)`), yielding each event out.
    This is what lets `messagebus.handle` find events without ever holding a direct reference to
    `patient` itself.
    
    ```python
    # service_layer/unit_of_work.py — AFTER
    import abc
    from adapters import repository
    
    class AbstractUnitOfWork(abc.ABC):
        patients: repository.AbstractRepository   # type annotation only — no object created here;
                                                  # the real repo is assigned in a concrete
                                                  # subclass's __enter__, e.g. SqlAlchemyUnitOfWork
    
        def __exit__(self, *args):
            self.rollback()
    
        def commit(self):
            self._commit()
    
        def collect_new_events(self):             # NEW
            for patient in self.patients.seen:     # every aggregate touched this transaction
                while patient.events:
                    yield patient.events.pop(0)     # drain one at a time, removing it
    ```
    
    **Why two separate collections:**
    
    ```python
    # illustration only — inside a handler function, e.g. service_layer/messageHandlers.py
    patient = uow.patients.get("p1")            # → added to .seen
    patient.update_phone_number(new_number)     # → appends PhoneNumberChanged to patient.events
    
    other = uow.patients.get("p2")              # → added to .seen
    # handler only reads other.name for a report — never mutates it
    
    # after the handler returns:
    uow.patients.seen == {patient, other}       # BOTH tracked — .seen fires on every fetch
    patient.events == [PhoneNumberChanged(...)] # has an event
    other.events   == []                        # touched, but nothing happened — empty
    ```
    
    `.seen` (on the repository) answers "which aggregates were touched?" — filled unconditionally
    the instant something is fetched. `.events` (on the aggregate root) answers "what actually
    happened?" — filled only when domain logic decides something notable occurred. `.seen` is a
    superset, which is why `collect_new_events` guards each object with `while patient.events:`
    rather than assuming every seen object carries something. Value objects (`PhoneNumber`,
    `TimeSlot`) never appear in either — they have no identity, are never fetched from a
    repository, and never hold events. Everything routes through the aggregate root.
    
    **Trade-off:** you lose the ability to just read `book_appointment()` top-to-bottom and see the
    whole flow — behavior is now spread across a registry, plus `.seen`'s implicit bookkeeping. In
    exchange, adding a new reaction to an existing event means adding one handler function and one
    line in `HANDLERS`, touching nothing else.
    
    ---
    
    ## Ch. 10 — Commands and Command Handler
    
    **Problem:** `AppointmentBooked` (something that happened, fire-and-forget, any number of
    listeners, failure is not catastrophic) and `BookAppointment` (a specific request that one
    handler must fulfill, and if it fails you need to know) had been treated identically — both
    just "events" thrown on the same bus. That blurring makes error handling and intent unclear.
    
    **Solution:** Split the concept into two types:
    
    |  | **Event** | **Command** |
    | --- | --- | --- |
    | Named as | past tense (`AppointmentBooked`, `SchedulingConflict`) | imperative (`BookAppointment`, `RegisterPatient`) |
    | Sent by | domain model, anyone | one specific caller with intent |
    | Handlers | zero, one, or many | exactly one |
    | On failure | swallow/log, don't blow up the whole flow | must raise — the caller needs to know |
    - Commands vs. Events: why they need different error handling
        
        ## The core distinction
        
        |  | **Command** | **Event** |
        | --- | --- | --- |
        | Meaning | "please do this" — a request | "this already happened" — a fact |
        | Example | `BookAppointment` | `AppointmentBooked` |
        | Handlers | exactly **one** | zero, one, or **many** |
        | Who cares if it fails | the caller, directly and immediately | your team/system, not the caller |
        | Correct failure behavior | **raise** — let it propagate | **catch, log, and continue** |
        
        The mistake to avoid: routing both through the same undifferentiated dispatch, with no
        `try/except` distinction between them.
        
        ## Why treating them the same is undesirable
        
        If a `BookAppointment` **command** succeeds — the booking is validated, committed to the
        database, real — but then a downstream **event** handler reacting to `AppointmentBooked`
        fails (say, a cron-scheduling job for a reminder, or a Redis publish), an undifferentiated
        bus lets that exception propagate all the way back up to the HTTP response.
        
        Consequences:
        
        - **The user sees an error for something that actually worked.** Their appointment is
        genuinely booked, but they get a 500. They might retry, risking confusion or a rejected
        double-booking attempt, and lose trust in the system.
        - **One flaky reaction can take down an unrelated request.** If three more handlers react to
        `AppointmentBooked` later (SMS, audit log, analytics), *any one* failing kills the whole
        request — none of which have anything to do with whether the booking itself succeeded.
        - **It undoes the reason events exist.** An event is a decoupled broadcast of a fact — its
        handlers are reactions, not owners of the original action's success. If a reaction's failure
        can invalidate delivery of that fact to the original caller, nothing has actually been
        decoupled — it's Ch. 8's tight coupling wearing a bus-shaped costume.
        
        The general principle: **the command tells the truth to the caller about the thing they
        asked for. Events tell the truth to your monitoring about everything that happened as a
        consequence.** Those are two different audiences. Mixing them back together defeats the
        purpose of putting reactions on a bus in the first place.
        
        ## How to handle each, correctly
        
        ### Commands — let them raise
        
        ```python
        def handle_command(command, uow):
            handler = COMMAND_HANDLERS[type(command)]
            handler(command, uow)      # NO try/except — if this fails, the caller must know
        ```
        
        The caller (an HTTP endpoint, a CLI, a test) is directly waiting on this outcome. If
        `book_appointment` fails — a real scheduling conflict, a DB error — that failure is the
        answer to "did my request succeed," and it should surface as a real error (e.g. Flask
        returning a 4xx/5xx), not be swallowed.
        
        ### Events — catch, log, monitor, and move on
        
        ```python
        import sentry_sdk
        
        def handle_event(event, uow):
            for handler in EVENT_HANDLERS[type(event)]:
                try:
                    handler(event, uow)
                except Exception:
                    logger.exception(f"Failed to handle event {event}")
                    sentry_sdk.capture_exception()          # visibility for engineers, not the user
                    metrics.increment(f"event_handler_failure.{handler.__name__}")
                    continue    # one bad handler must not block the rest of the queue
        ```
        
        To the **user**: nothing. No error, no indication anything went wrong — because from their
        point of view, nothing did. Their command succeeded; that's the truth and that's what they see.
        
        To the **system/engineers**: this is where the failure needs to actually go somewhere useful,
        not just vanish into a log file nobody reads:
        
        1. **Log** (`logger.exception`) — for after-the-fact debugging, with full traceback.
        2. **Alert/monitor** (e.g. Sentry, a metrics counter) — so a human finds out proactively
        instead of a silent failure nobody notices. A logged-and-forgotten failure is *worse* than
        a loud one, because nobody's looking for it.
        3. **Retry** — many of these failures are transient (a queue briefly unreachable, a network
        blip). A retry policy with backoff recovers most of them with no human involved.
        4. **Dead-letter / durable storage** — if retries exhaust, persist the failed event (a
        "failed events" table, a DLQ topic) instead of losing it, so it can be replayed manually
        or automatically later.
        
        ## One-line summary
        
        **Raise for commands, because the caller is standing there waiting for the answer. Catch,
        log, and monitor for events, because nobody is waiting — the fact already happened, and a
        failed reaction to it is your problem to notice and fix, not the user's problem to be told
        about.**
        
    
    ### BEFORE — one dict, one dispatch path, no distinction
    
    requests and facts alike sit in the same `HANDLERS` dict and run through the
    same loop, with no `try/except` anywhere. If a handler for a *fact* throws — say, publishing
    to Redis fails — that exception propagates all the way up through `handle()`, even though the
    booking itself already succeeded and was already committed to the database.
    
    ```python
    # service_layer/messagebus.py — BEFORE (Ch. 9's version)
    HANDLERS = {
        BookAppointment: [messageHandlers.book_appointment],
        AppointmentBooked: [messageHandlers.publish_appointment_booked_event],
        SchedulingConflict: [messageHandlers.send_conflict_notification],
    }
    
    def handle(message, uow):
        queue = [message]
        while queue:
            message = queue.pop(0)
            for handler in HANDLERS[type(message)]:
                handler(message, uow)          # no try/except — ANY failure propagates up
            queue.extend(uow.collect_new_events())
    ```
    
    Concretely: `book_appointment` succeeds and commits → `AppointmentBooked` gets yielded →
    `publish_appointment_booked_event` runs and Redis is down → that exception bubbles straight up
    through `handle()`, out of the Flask route, and the patient gets a 500 error for a booking
    that actually worked.
    
    ### AFTER — two registries, two dispatch functions, different failure rules
    
    commands and events become distinct types, each with their own dict and their
    own dispatch function. `handle_command` looks up exactly one handler and lets it raise — the
    caller of a command is directly waiting on that outcome. `handle_event` loops over
    zero-to-many handlers, wraps each call in `try/except`, logs failures, and continues to the
    next handler regardless — a broken reaction to a fact must never block the rest of the queue
    or the original caller.
    
    ```python
    # domain/commands.py — AFTER (new file)
    from dataclasses import dataclass
    from typing import Optional
    
    @dataclass
    class BookAppointment:
        patient_id: str
        slot_time: str
    
    @dataclass
    class RegisterPatient:
        patient_id: str
        name: str
        phone: Optional[str] = None
    ```
    
    ```python
    # service_layer/messagebus.py — AFTER: different handling per type
    import logging
    from domain.commands import Command
    from domain.events import Event
    
    logger = logging.getLogger(__name__)
    
    def handle(message, uow):
        queue = [message]
        while queue:
            message = queue.pop(0)
            if isinstance(message, Command):
                handle_command(message, uow)
            elif isinstance(message, Event):
                handle_event(message, uow)
            else:
                raise Exception(f"{message} is not an Event or Command")
    
    def handle_command(command, uow):
        handler = COMMAND_HANDLERS[type(command)]     # exactly ONE
        handler(command, uow)                          # let it raise on failure
    
    def handle_event(event, uow):
        for handler in EVENT_HANDLERS[type(event)]:    # zero, one, or MANY
            try:
                handler(event, uow)
            except Exception:
                logger.exception(f"Failed to handle event {event}")
                continue                                # don't kill the loop
    ```
    
    **What actually changed:** the same Redis-down scenario now results in a logged exception and
    a normal 202 response to the patient, because `publish_...` failing is caught inside
    `handle_event` and never reaches `handle_command`, which is the only path connected back to
    the Flask route.
    
    **Trade-off:** two registries and two dicts instead of one — more ceremony. But now a failing
    notification handler can't accidentally take down the booking command, and a caller of
    `BookAppointment` gets a real exception if booking fails, instead of it silently vanishing
    into a fire-and-forget event.
    
    ---
    
    ## Ch. 11 — Event-Driven Architecture: Using Events to Integrate Microservices
    
    <aside>
    💡
    
    Use only for events that does not need to be synchronous, ie. the caller service does not need to wait for the other service to finish executing before continue running.
    
    </aside>
    
    **Problem:** So far everything is in-process — one Python process, one message bus, one memory space. Real systems often need a **Scheduling** service and a **Reminders** service to talk to each other without a hard dependency (e.g. calling each other's REST APIs directly, which recreates tight coupling across process boundaries).
    
    **Solution:** Publish events externally (the book uses Redis pub/sub) instead of calling other
    services' APIs. Each service reacts to events it cares about — same in-process pattern, just
    crossing a network boundary via a message broker instead of an internal Python list.
    
    ### BEFORE — Scheduling calls Reminders' API directly
    
    `book_appointment` finishes committing the booking, then reaches out over HTTP
    straight into Reminders' own API, by URL, and waits for its response before returning.
    Scheduling now has a hard synchronous dependency on Reminders being reachable and fast.
    
    ```python
    # service_layer/messageHandlers.py — BEFORE (in the SCHEDULING service)
    import requests
    
    def book_appointment(cmd: BookAppointment, uow) -> str:
        with uow:
            patient = uow.patients.get(cmd.patient_id)
            slot = TimeSlot(cmd.slot_time)
            ref = patient.book(slot)
            uow.commit()
    
        # ↓ the direct cross-service call: hardcoded URL, synchronous, waits for a response
        requests.post(
            "https://reminders.internal/api/schedule",
            json={"patient_id": cmd.patient_id, "slot_time": cmd.slot_time, "ref": ref},
        )
        return ref
    ```
    
    ```python
    # entrypoints/flask_app.py — BEFORE (in the REMINDERS service)
    # Reminders must expose and maintain an HTTP endpoint purely for Scheduling to call
    from flask import Flask, request
    
    app = Flask(__name__)
    
    @app.route("/api/schedule", methods=["POST"])
    def schedule_endpoint():
        # Reminders' own domain work happens here
        schedule_sms(request.json["patient_id"], request.json["slot_time"])
        return {"status": "scheduled"}, 201    # ← this response is what Scheduling sits waiting for
    ```
    
    What breaks: if Reminders is down, deploying, or slow, `requests.post` throws or hangs *inside*`book_appointment` — before `return ref` — so the whole command fails even though the booking was already committed. Scheduling also has to know Reminders' exact URL, payload shape, and auth; adding a third consumer (Analytics) means bolting a second `requests.post` into this same function, repeating the growing-list problem from Ch. 8/9, now across the network.
    
    ### AFTER — Scheduling publishes a fact; Reminders subscribes on its own
    
    the `requests.post` line is deleted outright, not moved. Scheduling's existing
    event handler writes a message to a Redis channel instead — it targets no URL and doesn't know who, if anyone, is listening. On the other side, Reminders runs its own separate long-running process that subscribes to that channel, unpacks the incoming JSON, and calls its own service function to do the work. Reminders' HTTP endpoint disappears entirely.
    
    **Side 1 — the Scheduling service (publisher):**
    
    ```python
    # service_layer/messageHandlers.py — AFTER (SCHEDULING service)
    from domain.events import AppointmentBooked
    from adapters import redis_eventpublisher
    
    def book_appointment(cmd: BookAppointment, uow) -> str:
        with uow:
            patient = uow.patients.get(cmd.patient_id)
            slot = TimeSlot(cmd.slot_time)
            ref = patient.book(slot)
            uow.commit()
        return ref
        # no requests.post, no URL, no mention of Reminders — this function doesn't know it exists
    
    def publish_appointment_booked_event(event: AppointmentBooked, uow):
        redis_eventpublisher.publish("appointment_booked", event)
    ```
    
    ```python
    # adapters/redis_eventpublisher.py — AFTER (SCHEDULING service)
    import json
    import logging
    from dataclasses import asdict
    import redis
    import config
    from domain import events
    
    r = redis.Redis(**config.get_redis_host_and_port())
    
    def publish(channel, event: events.Event):
        logging.debug("publishing: channel=%s, event=%s", channel, event)
        r.publish(channel, json.dumps(asdict(event)))
        # returns as soon as Redis acks the PUBLISH — does NOT wait for any subscriber
    ```
    
    **Side 2 — the Reminders service (subscriber), a separate codebase and process:**
    
    ```python
    # entrypoints/redis_eventconsumer.py — AFTER (REMINDERS service)
    import json
    import redis
    import config
    from service_layer import services, unit_of_work
    
    def main():
        r = redis.Redis(**config.get_redis_host_and_port())
        pubsub = r.pubsub(ignore_subscribe_messages=True)
        pubsub.subscribe("appointment_booked")     # Reminders decides what it cares about
    
        for m in pubsub.listen():                   # BLOCKS here, waiting for Redis to push
            handle_appointment_booked(m)
    
    def handle_appointment_booked(m):
        data = json.loads(m["data"])                # the JSON Scheduling published
        services.schedule_reminder(                 # plain function call — Reminders has no bus
            patient_id=data["patient_id"],
            slot_time=data["slot_time"],
            uow=unit_of_work.SqlAlchemyUnitOfWork(),
        )
        # ^ Reminders' OWN uow, OWN database. It's a simple service — no commands, no registry.
    ```
    
    ```python
    # service_layer/services.py — AFTER (REMINDERS service, its own domain work)
    from datetime import timedelta
    from domain.model import Reminder
    
    def schedule_reminder(patient_id, slot_time, uow):
        with uow:
            reminder = Reminder(patient_id, send_at=slot_time - timedelta(hours=24))
            uow.reminders.add(reminder)
            uow.commit()
        # nothing is returned to Scheduling — there is no response channel back
    ```
    
    **Notice what is NOT here:** there is no response. `publish()` returned the moment Redis
    accepted the message; Scheduling never learns whether Reminders received it, processed it, or crashed. If Reminders ever *does* need to tell Scheduling something, it doesn't call back — it publishes its own event on its own channel, and Scheduling runs its own consumer subscribed to that:
    
    ```python
    # service_layer/services.py — REMINDERS service — reporting back is just another publish, not a reply
    def notify_reminder_failed(patient_id, reason):
        redis_eventpublisher.publish("reminder_failed", {"patient_id": patient_id, "reason": reason})
    
    # entrypoints/redis_eventconsumer.py — SCHEDULING service — has its own consumer for channels IT cares about
    def main():
        pubsub.subscribe("reminder_failed")
        for m in pubsub.listen():
            data = json.loads(m["data"])
            messagebus.handle(commands.FlagBookingIssue(**data), uow)
    ```
    
    So the shape is symmetric: **each service only ever publishes facts outward and subscribes to
    the facts it cares about.** Neither ever calls the other.
    
    ```
    In-process (Ch. 8-10):     domain event → in-memory list → messagebus.handle()
    Cross-service (Ch. 11):    domain event → redis publish → [network] → other service's
                                redis subscriber → messagebus.handle() (in THAT service)
    ```
    
    Two separate network hops, not one:
    
    ```
    [SCHEDULING process]              [Redis]                [REMINDERS process]
      publish("appointment_booked")
            │──── hop 1: PUBLISH ───────►│
            │                             │   pubsub.listen() already blocking here
      returns immediately.                │──── hop 2: push ───────────────►│
      Scheduling is DONE.                 │                     handle_appointment_booked(m)
                                          │                     → its own bus, db, domain
    ```
    
    **Trade-off:** you now have eventual consistency (Reminders reacts *sometime after* the event
    fires, not synchronously) and a new failure mode — messages can be missed, duplicated, or
    delayed (plain Redis pub/sub doesn't buffer, so a subscriber that's down at publish time loses
    the message; production systems harden this with a durable broker or an outbox pattern). Worth
    it because Scheduling doesn't need to know Reminders exists, or vice versa — they only need to
    agree on the shape of the event.
    
    ---
    
    ## Ch. 12 — Command-Query Responsibility Segregation (CQRS)
    
    - When should you actually reach for CQRS?
        
        <aside>
        💡
        
        QRS solves one problem: **the data shape that's correct for protecting business rules is usually not the data shape that's fast or convenient for answering questions.**
        
        </aside>
        
        "CQRS" isn't one single technique with one on/off switch — it's a spectrum, and the two ends
        of it have very different cost profiles. Knowing which end you're at changes whether the
        trade-off is worth it.
        
        **Level 1 — query segregation only (no duplicate table), compared against the baseline of
        *not* doing this at all (routing every read through `uow.patients.get(...)` and the full
        `Patient` aggregate, as in earlier chapters).** A plain function that queries the *existing*
        write-side tables directly with SQL, and returns dicts/DTOs instead of loading the aggregate:
        
        python
        
        ```python
        def appointments_view(patient_id, session):    return session.execute(        "SELECT slot_time, reference FROM appointments WHERE patient_id = :patient_id",        dict(patient_id=patient_id),    ).fetchall()
        ```
        
        **Pros (vs. reading through the aggregate):**
        
        - **Cheaper.** Skips reconstructing a full `Patient` object (and every `Appointment` inside
        it, and all its invariant-checking machinery) just to throw most of it away — one SQL query,
        plain dicts back.
        - **No repository or unit-of-work ceremony for something that isn't a transaction.** The
        aggregate path exists to protect mutations; a read isn't one, so paying for `with uow:`,
        `.seen` tracking, and object reconstruction was overhead with no corresponding benefit.
        - **Just as fresh.** Both approaches read the same live write-side data — Level 1 doesn't
        trade away any correctness to get its speed gain here, unlike Level 2 below.
        - **Answers shapes the aggregate can't.** A query spanning fields the aggregate doesn't
        expose, or filtering criteria the aggregate wasn't built to support, is just another SQL
        `WHERE` clause here — no method needs to be added to `Patient` for the read to work.
        
        **Cons (vs. reading through the aggregate):**
        
        - **One more place that breaks on a schema change.** Before Level 1, if the underlying tables
        changed shape, only the repository's own query-building code (`_get`/`_add`) had to be
        updated — every caller of `uow.patients.get(...)` kept working untouched, because they only
        ever dealt with `Patient` objects, never the raw tables. Now that this view function also
        writes SQL directly against those same tables, it's a second place that has to be found and
        rewritten whenever the schema changes — the repository no longer absorbs that change alone.
        - **Every new screen means a brand new query, written from scratch.** With the aggregate,
        there was exactly one fetch to call no matter what you needed to show —
        `uow.patients.get(patient_id)` — and then you'd pull whatever fields you wanted out of the
        `Patient` object in Python. Need appointments? Walk `.appointments`. Need the phone number
        too? It's already sitting right there on the same object. One function covered every read
        shape, because the object already had everything loaded.
        With Level 1, each differently-shaped screen needs its own hand-written SQL, because a plain
        query only returns exactly the columns you asked for:
        
        python
        
        ```python
          def appointments_view(patient_id, session):      return session.execute(          "SELECT slot_time, reference FROM appointments WHERE patient_id = :patient_id",          dict(patient_id=patient_id),      ).fetchall()  def appointments_with_provider_view(patient_id, session):   # a SECOND, separate query —      return session.execute(                                  # can't reuse the one above          """          SELECT a.slot_time, a.reference, p.name          FROM appointments a JOIN providers p ON p.id = a.provider_id          WHERE a.patient_id = :patient_id          """,          dict(patient_id=patient_id),      ).fetchall()
        ```
        
        Every new read shape is its own query to write and maintain — nothing is shared between
        them the way `Patient` was shared between every use of the aggregate.
        
        - **Any logic living on `Patient` has to be manually copied into every raw query.** Say
        `Patient` grows a rule like "cancelled appointments should never show up" — expressed as a
        property on the domain model:
        
        python
        
        ```python
          class Patient:      @property      def active_appointments(self):          return [a for a in self.appointments if not a.cancelled]
        ```
        
        Anyone reading through the aggregate gets this for free just by calling
        `patient.active_appointments` — the filtering happens automatically, in one place, no matter
        who's asking.
        
        A Level 1 query has no connection to `Patient` at all, so it doesn't know this rule exists.
        Whoever wrote `appointments_view` has to remember to add `WHERE cancelled = false` themselves:
        
        python
        
        ```python
          def appointments_view(patient_id, session):      return session.execute(          "SELECT slot_time, reference FROM appointments "          "WHERE patient_id = :patient_id AND cancelled = false",   # easy to forget          dict(patient_id=patient_id),      ).fetchall()
        ```
        
        If a second screen adds its own query and forgets that same `WHERE` clause, it'll silently
        show cancelled appointments that the aggregate would never have exposed — the rule isn't
        centralized anywhere Level 1 queries automatically pick it up from.
        
        **Level 2 — a separate read model / projection, compared against the baseline of Level 1
        alone.** A genuinely new, denormalized table (`appointments_view` as its own table), populated
        and kept in sync by an event handler, as built out earlier in this chapter.
        
        **Pros (vs. Level 1 alone):**
        
        - **Handles expensive queries Level 1 can't make cheap.** If the read genuinely needs to join
        across multiple aggregates (patient + provider + clinic) or aggregate over many rows on
        every request (a dashboard), Level 1's raw query against normalized tables can still be
        slow — Level 2 pre-computes that cost once, at write time, instead of paying it on every
        read.
        - **Answers queries with no natural starting point.** "All appointments today across every
        patient" has no single row or aggregate to query from in the normalized write tables in a
        cheap way — a dedicated table built for exactly that shape does.
        - **Fully decouples the read schema from the write schema.** Level 1 still queries the real
        write tables directly, so it's still exposed to write-side refactors. A separate projection
        is insulated from that — the write side can change internal structure freely as long as the
        event payload stays the same.
        - **Scales independently under high read traffic.** The read table can be indexed, cached, or
        even hosted differently from the write database, without touching write-side performance at
        all — Level 1 still competes with writes for the same tables.
        
        **Cons (vs. Level 1 alone):**
        
        - **A second table to create, migrate, and index**, on top of the write-side tables — real
        schema surface area that Level 1 never required.
        - **A staleness window that Level 1 doesn't have.** The read table is only as fresh as the
        last time its event handler ran. A patient could book an appointment and, for a brief
        moment, not see it reflected on a screen reading from the projection — Level 1 has zero lag
        because it reads the live tables directly.
        - **Two schemas to reconcile mentally**, where Level 1 only ever had one. Anyone debugging
        "why does this screen show the wrong data" now has to know there are two representations of
        the same fact and check both.
    - Worked example: splitting the `Patient` aggregate with CQRS
        
        Everything above was abstract. Here's one concrete aggregate, one concrete read requirement
        that doesn't fit it, and the full split from start to finish.
        
        **Step 1 — the aggregate, as the write side needs it.** `Patient` exists to protect one rule:
        no double-booking. To check that, it needs its appointments loaded as real objects it can run
        logic across — that's the whole reason the aggregate is shaped the way it is.
        
        python
        
        ```python
        # domain/model.pyclass Patient:    def __init__(self, patient_id, appointments):        self.patient_id = patient_id        self.appointments = appointments   # real Appointment objects, needed to check conflicts        self.events = []    def book(self, slot: TimeSlot) -> str:        if any(appt.conflicts_with(slot) for appt in self.appointments):            self.events.append(SchedulingConflict(self.patient_id))            return None        appt = Appointment(slot)        self.appointments.append(appt)        self.events.append(AppointmentBooked(self.patient_id, slot.time, appt.reference))        return appt.reference
        ```
        
        **Step 2 — a read requirement that doesn't fit this shape at all.** Say the front desk wants a
        dashboard: *every appointment happening today, across every patient, with the patient's name
        and the provider's name attached.* Notice what's wrong with this from `Patient`'s point of
        view — there is no single `patient_id` to start from. The aggregate boundary was drawn around
        *one* patient; this query needs to cut straight across all of them.
        
        **Step 3 — why routing this through the aggregate doesn't work.** The only tool the aggregate
        gives you is `uow.patients.get(patient_id)` — fetch one patient at a time. To answer the
        dashboard query this way you'd have to do something like:
        
        python
        
        ```python
        # THE ONLY WAY to do this through the aggregate — and it's baddef todays_dashboard(all_patient_ids, uow):    results = []    with uow:        for pid in all_patient_ids:                    # load EVERY patient aggregate            patient = uow.patients.get(pid)              # full object reconstruction, every time            for appt in patient.appointments:                if appt.slot.time.date() == today():                    results.append((patient.name, appt.slot.time, appt.reference))    return results
        ```
        
        This loads every patient's full aggregate — batches, invariant-checking machinery, all of
        it — just to throw away everything except three fields per matching row, and it still doesn't
        have the provider's name, because `Provider` is a different aggregate the `Patient` object was
        never built to reach into. This is the exact mismatch from earlier: a shape built to protect a
        write-time rule, forced to answer a read it was never designed for.
        
        **Step 4 — split it. Writes stay untouched; reads get their own model.**
        
        python
        
        ```python
        # entrypoints/flask_app.py (or any caller) — WRITE side — completely unchangedfrom domain import commandsfrom service_layer import messagebusmessagebus.handle(commands.BookAppointment("p-99", "2026-09-01T09:00"), uow)
        ```
        
        sql
        
        ```sql
        -- a new, purpose-built table — shaped for exactly this dashboard, nothing elseCREATE TABLE todays_dashboard_view (    appointment_id   TEXT PRIMARY KEY,    patient_name     TEXT,    provider_name    TEXT,    slot_time        TIMESTAMP,    reference         TEXT);
        ```
        
        python
        
        ```python
        # adapters/repository.py — the repository method that keeps the read table currentdef add_to_dashboard_view(session, appointment_id, patient_name, provider_name, slot_time, reference):    session.execute(        """        INSERT INTO todays_dashboard_view            (appointment_id, patient_name, provider_name, slot_time, reference)        VALUES (:appointment_id, :patient_name, :provider_name, :slot_time, :reference)        """,        dict(appointment_id=appointment_id, patient_name=patient_name,             provider_name=provider_name, slot_time=slot_time, reference=reference),    )# the READ query itself — no aggregate, no repository.get(), just a flat lookupdef todays_dashboard(session):    return session.execute(        "SELECT patient_name, provider_name, slot_time, reference "        "FROM todays_dashboard_view WHERE slot_time::date = CURRENT_DATE"    ).fetchall()
        ```
        
        python
        
        ```python
        # service_layer/eventHandlers.py — the SAME AppointmentBooked event, one more reactionfrom domain.events import AppointmentBookedfrom adapters import repositorydef handle_appointment_booked_dashboard(event: AppointmentBooked, uow):    with uow:        patient_name = uow.patients.get(event.patient_id).name   # cheap: one small lookup        provider_name = lookup_provider_name(event.provider_id)   # from the Provider aggregate        repository.add_to_dashboard_view(            uow.session, event.reference, patient_name, provider_name,            event.slot_time, event.reference,        )        uow.commit()EVENT_HANDLERS = {    AppointmentBooked: [        publish_appointment_booked_event,          # Ch. 11 — tell Reminders        handle_appointment_booked_read_model,       # Ch. 12 — this patient's own appointments        handle_appointment_booked_dashboard,        # NEW — the cross-patient dashboard    ],}
        ```
        
        **Step 5 — what changed, and what didn't.** `Patient.book` was never touched — it still only
        knows how to protect its own invariant. The dashboard's cross-aggregate join (patient name +
        provider name, spanning two different aggregates) happens exactly once, at booking time,
        inside the event handler — not on every dashboard page load. The read itself is now a single
        flat `SELECT` with no joins, no aggregate loading, and no knowledge of `Patient` or `Provider`
        at all.
        
        ```
        Write path:   BookAppointment → book_appointment handler → Patient.book() → AppointmentBooked
        Read paths:   AppointmentBooked ──► publish_appointment_booked_event   (Ch. 11: cross-service)
                                        ──► handle_appointment_booked_read_model (Ch. 12: this patient)
                                        ──► handle_appointment_booked_dashboard  (this example: cross-patient)
        ```
        
        One write-time fact, fanning out into as many independently-shaped read models as there are
        distinct query needs — each one paying its own denormalization cost once, so its matching read
        never has to.
        
    
    **Problem:** Building a UI screen ("show all of a patient's upcoming appointments") through the
    domain model and repository means loading full aggregates, walking through business objects,
    and reassembling something that's ultimately just a read-only join — heavy machinery for
    something with no business logic in it at all.
    
    **Solution:** Split reads from writes entirely. Writes still go through commands → domain
    model → events (everything above). Reads bypass all of that and hit the database (or a
    denormalized read-only view) directly.
    
    ### BEFORE — reads go through the same aggregate machinery as writes
    
    to answer "what appointments does this patient have," you fetch the full
    `Patient` aggregate — the same object writes use, with all its invariant-checking machinery —
    just to walk its `.appointments` list and pull two fields off each item. None of the
    conflict-checking logic is relevant to this query, but you load it anyway.
    
    ```python
    # service_layer/services.py — BEFORE: reads reuse the write-side aggregate
    def appointments_view(patient_id, uow):
        with uow:
            patient = uow.patients.get(patient_id)   # loads the FULL aggregate
            return [
                {"slot_time": appt.slot.time, "reference": appt.reference}
                for appt in patient.appointments
            ]
    ```
    
    ### AFTER — reads bypass the domain model entirely
    
    writes are untouched — commands still flow through the bus into the domain
    model exactly as before. Reads become a plain query method against a separate, denormalized
    table built to answer this one query fast: no domain model, no unit of work, no aggregate
    loading — the method lives on the repository, but returns rows/dicts instead of a `Patient`.
    That table is kept current by a **repository method, called from an event handler** — the
    same `AppointmentBooked` event from Ch. 9/11 now also triggers a second, independent reaction
    that writes a flat row.
    
    ```python
    # entrypoints/flask_app.py (or any caller) — Write side — AFTER: unchanged, full weight of the pattern
    from domain import commands
    from service_layer import messagebus
    
    messagebus.handle(commands.BookAppointment("p-99", "2026-09-01T09:00"), uow)
    ```
    
    ```python
    # adapters/repository.py — Read side — AFTER: no domain model, no UoW, no aggregate loading. Just a query method.
    from typing import List
    from sqlalchemy.orm import Session
    
    def appointments_view(patient_id: str, session: Session) -> List[dict]:
        return session.execute(
            """
            SELECT slot_time, reference FROM appointments_view WHERE patient_id = :patient_id
            """,
            dict(patient_id=patient_id),
        ).fetchall()
    ```
    
    ```python
    # adapters/repository.py — the read model is KEPT UP TO DATE by a repository method, not queried live from the aggregate
    def add_booking_to_read_model(session, patient_id, slot_time, reference):
        session.execute(
            """
            INSERT INTO appointments_view (patient_id, slot_time, reference)
            VALUES (:patient_id, :slot_time, :reference)
            """,
            dict(patient_id=patient_id, slot_time=slot_time, reference=reference),
        )
    ```
    
    ```python
    # service_layer/eventHandlers.py — calls the repository method above; owns the transaction and the registry
    from domain.events import AppointmentBooked
    from adapters import repository
    from service_layer.messageHandlers import publish_appointment_booked_event
    
    def handle_appointment_booked_read_model(event: AppointmentBooked, uow):
        with uow:
            repository.add_booking_to_read_model(
                uow.session, event.patient_id, event.slot_time, event.reference
            )
            uow.commit()
    
    EVENT_HANDLERS = {
        AppointmentBooked: [publish_appointment_booked_event, handle_appointment_booked_read_model],
    }
    ```
    
    ```
    Write path: Command → Handler → Domain Model → Event(s)
    Read path:  Query → raw SQL against a read-optimized table, kept in sync by an event handler
    ```
    
    **One sentence:** the *same* `AppointmentBooked` event that triggers a Redis publish (Ch. 11)
    also triggers a totally separate handler that updates a flat, query-friendly table — reads and
    writes never touch the same code path again.
    
    **Trade-off:** two models of the same data to keep in sync (and the read model can lag briefly
    behind the write model). Justified when read patterns are complex/frequent and don't map
    cleanly onto the aggregate structure — overkill for simple CRUD screens.
    
    ---
    
    ## Ch. 13 — Dependency Injection (and Bootstrapping)
    
    **Problem:** By this point every handler needs a UoW, some need a notifications abstraction,
    some need a publisher — and every test, every entrypoint (Flask, Redis consumer, CLI) was
    manually constructing and wiring all of that by hand. Tests were passing fakes positionally
    and it was getting fragile — swap the argument order in one handler and imports break silently.
    
    **Solution:** A single `bootstrap.py` wires up the dependencies once, and injects them into
    handlers via `functools.partial` — so callers just call `bus.handle(message)` without knowing
    what each handler internally needs.
    
    ### BEFORE — every caller wires dependencies manually
    
    handlers construct their own infrastructure inline, and every entrypoint and
    test independently builds a `uow` and passes it around by hand. Nothing centralizes it, so
    swapping a real dependency for a fake means editing every place that constructs one.
    
    ```python
    # service_layer/messageHandlers.py — BEFORE
    from domain.events import SchedulingConflict
    from adapters.notifications import EmailNotifications
    
    def send_conflict_notification(event: SchedulingConflict, uow):
        notifications = EmailNotifications()          # constructed INSIDE the handler — unswappable
        notifications.send(f"Conflict for patient {event.patient_id}")
    ```
    
    ```python
    # entrypoints/flask_app.py — BEFORE
    from flask import Flask, request
    from service_layer import messagebus, unit_of_work
    from domain.commands import BookAppointment
    
    app = Flask(__name__)
    
    @app.route("/book", methods=["POST"])
    def book_endpoint():
        uow = unit_of_work.SqlAlchemyUnitOfWork()      # constructed by hand, in every entrypoint
        cmd = BookAppointment(request.json["patient_id"], request.json["slot_time"])
        messagebus.handle(cmd, uow)
        return "OK", 202
    ```
    
    ```python
    # tests/test_booking.py — BEFORE: fakes constructed and threaded through by hand, everywhere
    from service_layer import messagebus
    from tests.fakes import FakeUnitOfWork
    
    uow = FakeUnitOfWork()
    messagebus.handle(BookAppointment("p1", "09:00"), uow)
    # and nothing can fake EmailNotifications at all — it's hardcoded inside the handler
    ```
    
    ### AFTER — one bootstrap function wires everything, once
    
    handlers stop constructing dependencies and declare them as parameters instead.
    `bootstrap.py` inspects each handler's signature, matches parameter names against a dict of
    real (or fake) dependencies, and pre-binds only the ones that handler actually asks for. It
    returns a fully-wired `bus`. Every entrypoint and test calls `bootstrap()` once and then just
    calls `.handle(message)`.
    
    ```python
    # service_layer/messageHandlers.py — AFTER: handlers declare what they depend on
    from domain.events import SchedulingConflict
    from adapters.notifications import AbstractNotifications
    
    def send_conflict_notification(event: SchedulingConflict, uow,
                                   notifications: AbstractNotifications):
        notifications.send(f"Conflict for patient {event.patient_id}")
    ```
    
    ```python
    # bootstrap.py — AFTER (new file)
    import inspect
    from typing import Callable
    from adapters import orm, redis_eventpublisher
    from adapters.notifications import AbstractNotifications, EmailNotifications
    from service_layer import messageHandlers, messagebus, unit_of_work
    
    def bootstrap(
        start_orm: bool = True,
        uow: unit_of_work.AbstractUnitOfWork = unit_of_work.SqlAlchemyUnitOfWork(),
        notifications: AbstractNotifications = EmailNotifications(),
        publish: Callable = redis_eventpublisher.publish,
    ) -> messagebus.MessageBus:
    
        if start_orm:
            orm.start_mappers()
    
        dependencies = {"uow": uow, "notifications": notifications, "publish": publish}
        injected_event_handlers = {
            event_type: [
                inject_dependencies(handler, dependencies)
                for handler in event_handlers
            ]
            for event_type, event_handlers in messageHandlers.EVENT_HANDLERS.items()
        }
        injected_command_handlers = {
            command_type: inject_dependencies(handler, dependencies)
            for command_type, handler in messageHandlers.COMMAND_HANDLERS.items()
        }
    
        return messagebus.MessageBus(
            uow=uow,
            event_handlers=injected_event_handlers,
            command_handlers=injected_command_handlers,
        )
    
    def inject_dependencies(handler, dependencies):
        params = inspect.signature(handler).parameters
        deps = {name: dep for name, dep in dependencies.items() if name in params}
        return lambda message: handler(message, **deps)
    ```
    
    ```python
    # entrypoints/flask_app.py — AFTER
    from flask import Flask, request
    import bootstrap
    from domain import commands
    
    app = Flask(__name__)
    bus = bootstrap.bootstrap()             # wired ONCE, at startup
    
    @app.route("/book", methods=["POST"])
    def book_endpoint():
        cmd = commands.BookAppointment(request.json["patient_id"], request.json["slot_time"])
        bus.handle(cmd)                      # route no longer constructs a uow at all
        return "OK", 202
    ```
    
    ```python
    # tests/test_booking.py — swap real deps for fakes at ONE point, everything downstream just works
    import bootstrap
    from tests.fakes import FakeUnitOfWork, FakeNotifications
    
    bus = bootstrap.bootstrap(
        start_orm=False,
        uow=FakeUnitOfWork(),
        notifications=FakeNotifications(),
        publish=lambda *args: None,
    )
    ```
    
    **One sentence:** bootstrap is the *only* place in the whole app that knows about real
    implementations (SQLAlchemy, email, Redis) — everything downstream (Flask routes, Redis
    consumer, tests) just gets a fully-wired `bus` and calls `.handle()`.
    
    **Trade-off:** an extra layer of magic (`inject_dependencies` inspecting function signatures)
    that can be confusing to a newcomer reading the codebase cold — you now need to understand
    bootstrap.py before you understand how any handler actually gets its arguments.
    
    ---
    
    ## Epilogue: Applying These Patterns in the Real World
    
    Not a new pattern — a check on when to *use* everything above.
    
    **The book's own caution, paraphrased:** these patterns pay off in domains with real, changing
    business logic and multiple developers touching the codebase over time. They cost real
    complexity — abstractions, indirection, more files, more registries.
    
    Rough decision heuristic (matches your Ch. 2 trade-off note):
    
    | Signal | Lean toward |
    | --- | --- |
    | CRUD app, thin logic, small team | Skip most of this — direct ORM + views is fine |
    | Business rules keep changing and getting tangled with infra | Repository + Service Layer at minimum |
    | Multiple side effects per action, growing | Add the message bus (Ch. 8–10) |
    | Multiple services need to react to the same business fact | Events over a broker (Ch. 11) |
    | Read patterns are complex and don't match your write model | CQRS (Ch. 12) |
    | Wiring dependencies by hand everywhere becomes unmanageable | Bootstrap/DI (Ch. 13) |
    
    **Full picture, end to end:**
    
    ```
    HTTP / CLI / Redis consumer  (entrypoints)
            ↓
       bootstrap.py  (wires real or fake dependencies once)
            ↓
         MessageBus  (routes Commands → 1 handler, Events → 0..N handlers)
            ↓                              ↓
     Command Handlers                Event Handlers
            ↓                              ↓
       Domain Model  (Aggregates, raises new Events)  ←──┘
            ↓
          UoW  (atomic transaction boundary)
            ↓
       Repository  (storage abstraction)
            ↓
         ORM / DB  ←→  Redis pub/sub (cross-service events)  ←→  Read model (CQRS)
    ```
    
    Every arrow in that diagram is the same idea repeated at a different scale: **depend on an
    abstraction, not the concrete thing**, so each box can be swapped, tested in isolation, or
    removed if it's not earning its keep.