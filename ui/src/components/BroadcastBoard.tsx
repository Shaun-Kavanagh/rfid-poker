import { CardType } from "@/components/Cards";
import { BroadcastCard } from "@/components/BroadcastPlayer";

// Community cards, centred along the bottom edge between the player columns.
const BroadcastBoard = ({ cards }: { cards: CardType[] }) => {
    const dealt = cards.filter(card => card && card.suit && card.rank);
    if (dealt.length === 0) {
        return null;
    }

    return (
        <div className="absolute bottom-5 left-1/2 -translate-x-1/2 bg-black bg-opacity-90 rounded-lg p-3 flex gap-3 shadow-lg">
            {dealt.map((card, index) => (
                <BroadcastCard key={index} card={card} large />
            ))}
        </div>
    );
};

export default BroadcastBoard;
