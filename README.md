# [isbaileybutlerintheoffice.today](https://isbaileybutlerintheoffice.today)?

---

Service to consume banking transaction data from my [balance project](https://github.com/baely/balance).

Transaction criteria for determining office presence:
 - Transaction amount is between $4.00 and $7.00
 - ~~Transaction time is between 6:00am and 12:00pm~~
 - Transaction is on a weekday
 - Transaction is not a foreign transaction
 - Transaction is categorised as "Restaurants and Cafes"

If the latest transaction matching those criteria is less than 12 hours old, then I am assumed to be in the office on that day.

### Sequence Diagrams

Storing valid transaction data

```mermaid
sequenceDiagram
    participant Up as Up Bank
    box gray Balance
    participant BalanceIn as Listener
    participant BalancePS as Pub/Sub
    participant BalanceDo as Processor
    participant BalanceDB as Firestore
    end
    box gray isbaileybutlerintheoffice.today
    participant OfficerIn as Listener
    participant OfficerDB as Firestore
    participant OfficerAPI as API
    end
    participant User

    Up ->> BalanceIn: Post event
    activate BalanceIn
    BalanceIn ->> BalancePS: Publish event
    BalanceIn -->> Up: Return OK
    deactivate BalanceIn
    BalancePS ->> BalanceDo: Push event
    activate BalanceDo
    BalancePS ->> Up: Retrieve Transaction & Account details
    Up -->> BalancePS: Returns Transaction & Account details
    BalanceDo ->> BalanceDB: Store enriched details
    BalanceDo ->> OfficerIn: Post enriched details
    activate OfficerIn
    opt Is qualifying transaction
    OfficerIn ->> OfficerDB: Store enriched details
    end
    deactivate OfficerIn
    deactivate BalanceDo
```

Presenting office presence

```mermaid
sequenceDiagram
    participant Up as Up Bank
    box gray Balance
    participant BalanceIn as Listener
    participant BalancePS as Pub/Sub
    participant BalanceDo as Processor
    participant BalanceDB as Firestore
    end
    box gray isbaileybutlerintheoffice.today
    participant OfficerIn as Listener
    participant OfficerDB as Firestore
    participant OfficerAPI as API
    end
    participant User

    User ->> OfficerAPI: GET /
    activate OfficerAPI
    OfficerAPI ->> OfficerDB: Retrieve recent transaction
    alt Transaction is recent
    OfficerAPI -->> User: "yes"
    else
    OfficerAPI -->> User: "no"
    end
    deactivate OfficerAPI
```
