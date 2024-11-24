# OpTrack
- Quick webapp to track version updates of OpenShift operators on quay.io.

---

When creating a new ticket, you can enter the operator repositories you wish to track in namespace/repository format.

<img width="605" alt="New Ticket png" src="https://github.com/user-attachments/assets/38701619-a314-4766-bcd3-72c66dfef4ff">

Once the ticket is added the app will create a JSON file on your local filesystem allowing you to close and restart the application where you left off.
When clicking on the Ticket name/number you will be able to view the current state of the operators as per Quay.io's API.

<img width="607" alt="Status Check png" src="https://github.com/user-attachments/assets/31fafda4-9cc0-4434-bec3-1bc115f87257">
