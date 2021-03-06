# todaysvote.ca

*Stay up to date with what Canada’s House of Commons is voting on each day.*

Today's Vote was written both because I wanted it to exist for myself and I thought it would be a good entry to programming with Go. It makes it easier to see how Canada's elected representatives are voting as soon as they do.

The worker does all of the hard stuff to make the server's job easy. First it pulls and digests XML data from the Parliament of Canada site. It stores it in a Postgres table, although nothing is really done with that yet. I'm currently using the Bing Translation API (I wrote a little [package](http://github.com/interstateone/translate) for this) to massage the vote descriptions that are provided, as they're faulty. Next it renders three files: a JSON feed, an RSS feed and the static HTML homepage. Lastly it fires any new vote information to Buffer (made another [package](http://github.com/interstateone/bufferapi)) to be posted on Twitter at a set interval throughout the day. This gets cross-compiled for linux/386 and run daily on some boring ol' shared hosting.

If you notice any issues or have feedback on the code, please open an issue or get in touch with me on [Twitter](http://twitter.com/interstateone).

Today's Vote is released under an MIT license, see the LICENSE file for more info.
